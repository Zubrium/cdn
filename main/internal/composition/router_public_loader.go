package composition

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	loaderapi "github.com/Zubrium/cdn/engines/loader/features/files/pkg"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

type routerPublicLoaderProvider struct {
	guard         routerPublicModuleGuard
	api           routerPublicLoaderAPI
	staging       routerPublicStaging
	authorization routerPublicAuthorization
	credential    string
	partSize      int64
	maxFileBytes  int64
	maxParts      int
}

func (provider *routerPublicLoaderProvider) authenticate(ctx context.Context) error {
	if err := provider.guard.requireModule(ctx, routerPublicLoaderModuleID); err != nil {
		return err
	}
	if isNilRouterPublicValue(provider.api) || isNilRouterPublicValue(provider.staging) || isNilRouterPublicValue(provider.authorization) {
		return contracts.ErrPublicUnavailable
	}
	return nil
}

func (provider *routerPublicLoaderProvider) serviceIdentity(ctx context.Context) (context.Context, contracts.AuthorizationSubject, error) {
	subject, principal, err := provider.authorization.Authenticate(ctx, provider.credential)
	if err != nil {
		return nil, contracts.AuthorizationSubject{}, mapRouterPublicAccessError(err)
	}
	if !principal.Active {
		return nil, contracts.AuthorizationSubject{}, contracts.ErrPublicUnauthorized
	}
	loaderCtx, err := provider.api.Authenticate(ctx, provider.credential)
	if err != nil || loaderCtx == nil {
		return nil, contracts.AuthorizationSubject{}, mapLoaderPublicError(err)
	}
	return loaderCtx, subject, nil
}

func (provider *routerPublicLoaderProvider) beginUpload(ctx context.Context, request contracts.PublicLoaderBeginUploadRequest) (contracts.PublicLoaderUploadSession, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicLoaderUploadSession{}, err
	}
	if request.PartSize != provider.partSize || request.MaxBytes <= 0 || request.MaxBytes > provider.maxFileBytes {
		return contracts.PublicLoaderUploadSession{}, contracts.ErrPublicInvalid
	}
	expected := expectedRouterPublicParts(request.MaxBytes, request.PartSize)
	if expected > provider.maxParts {
		return contracts.PublicLoaderUploadSession{}, contracts.ErrPublicTooLarge
	}
	capability, ok := contracts.StreamCapabilityFromContext(ctx)
	if !ok || capability == nil {
		return contracts.PublicLoaderUploadSession{}, contracts.ErrStreamUnavailable
	}
	body, err := capability.OpenRead(ctx, request.Stream)
	if err != nil {
		return contracts.PublicLoaderUploadSession{}, mapRouterPublicStreamError(err)
	}
	defer body.Close()

	loaderCtx, subject, err := provider.serviceIdentity(ctx)
	if err != nil {
		return contracts.PublicLoaderUploadSession{}, err
	}
	if err := provider.authorization.RequireGroupLevel(ctx, subject, request.Groups, contracts.AccessLevelWrite); err != nil {
		return contracts.PublicLoaderUploadSession{}, mapRouterPublicAccessError(err)
	}

	parts, total, checksum, err := provider.readAndStageParts(ctx, subject, body, request)
	if err != nil {
		provider.purgeStagedParts(ctx, parts)
		return contracts.PublicLoaderUploadSession{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			provider.purgeStagedParts(ctx, parts)
		}
	}()

	session, err := provider.api.BeginUpload(loaderCtx, loaderapi.BeginUploadInput{
		IdempotencyKey: request.IdempotencyKey, Name: request.Name, ContentType: request.ContentType,
		Size: total, SHA256: checksum, Groups: request.Groups, Attributes: request.Attributes,
	})
	if err != nil {
		return contracts.PublicLoaderUploadSession{}, mapLoaderPublicError(err)
	}
	if !validRouterPublicUUID(session.ID) || session.PartSize != request.PartSize {
		return contracts.PublicLoaderUploadSession{}, contracts.ErrPublicUnavailable
	}
	if session.State != "uploading" && session.State != "committing" && session.State != "committed" {
		return contracts.PublicLoaderUploadSession{}, contracts.ErrPublicUnavailable
	}
	if session.State == "uploading" {
		for _, part := range parts {
			staged, size, stagedChecksum, readErr := provider.staging.ReadStagedObject(ctx, subject, part.ObjectID)
			if readErr != nil {
				_ = provider.api.AbortUpload(loaderCtx, session.ID)
				return contracts.PublicLoaderUploadSession{}, mapLoaderPublicError(readErr)
			}
			if size != part.Size || stagedChecksum != part.SHA256 {
				_ = staged.Close()
				_ = provider.api.AbortUpload(loaderCtx, session.ID)
				return contracts.PublicLoaderUploadSession{}, contracts.ErrPublicUnavailable
			}
			_, putErr := provider.api.PutUploadPart(loaderCtx, session.ID, part.Number, part.Size, part.SHA256, staged)
			_ = staged.Close()
			if putErr != nil {
				_ = provider.api.AbortUpload(loaderCtx, session.ID)
				return contracts.PublicLoaderUploadSession{}, mapLoaderPublicError(putErr)
			}
		}
	}
	cleanup = false
	return publicLoaderUploadSession(session, parts, total, request.PartSize), nil
}

func (provider *routerPublicLoaderProvider) readAndStageParts(ctx context.Context, subject contracts.AuthorizationSubject, body io.Reader,
	request contracts.PublicLoaderBeginUploadRequest) ([]contracts.PublicLoaderStagedPart, int64, string, error) {
	var parts []contracts.PublicLoaderStagedPart
	var total int64
	digest := sha256.New()
	buffer := make([]byte, request.PartSize)
	for {
		if err := ctx.Err(); err != nil {
			return parts, 0, "", err
		}
		count, readErr := readRouterPublicPart(body, buffer)
		if count > 0 {
			if total > request.MaxBytes-int64(count) {
				return parts, 0, "", contracts.ErrPublicTooLarge
			}
			total += int64(count)
			if request.DeclaredSize >= 0 && total > request.DeclaredSize {
				return parts, 0, "", contracts.ErrPublicTooLarge
			}
			if len(parts) >= provider.maxParts {
				return parts, 0, "", contracts.ErrPublicTooLarge
			}
			partSum := sha256.Sum256(buffer[:count])
			part := contracts.PublicLoaderStagedPart{Number: len(parts) + 1, Size: int64(count), SHA256: hex.EncodeToString(partSum[:])}
			objectID, err := uuid.NewRandom()
			if err != nil {
				return parts, 0, "", contracts.ErrPublicUnavailable
			}
			part.ObjectID = objectID.String()
			_, _ = digest.Write(buffer[:count])
			if err := provider.staging.PutStagedObject(ctx, subject, part.ObjectID, request.Groups, part.Size, part.SHA256, bytes.NewReader(buffer[:count])); err != nil {
				return parts, 0, "", mapLoaderPublicError(err)
			}
			parts = append(parts, part)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			if errors.Is(readErr, contracts.ErrPublicTooLarge) {
				return parts, 0, "", contracts.ErrPublicTooLarge
			}
			return parts, 0, "", contracts.ErrPublicUnavailable
		}
		if count == 0 {
			return parts, 0, "", io.ErrNoProgress
		}
	}
	if request.DeclaredSize >= 0 && total != request.DeclaredSize {
		return parts, 0, "", contracts.ErrPublicInvalid
	}
	return parts, total, hex.EncodeToString(digest.Sum(nil)), nil
}

func (provider *routerPublicLoaderProvider) purgeStagedParts(ctx context.Context, parts []contracts.PublicLoaderStagedPart) {
	for _, part := range parts {
		_ = provider.staging.PurgeStagedObject(ctx, part.ObjectID)
	}
}

func readRouterPublicPart(reader io.Reader, buffer []byte) (int, error) {
	var total int
	for total < len(buffer) {
		count, err := reader.Read(buffer[total:])
		total += count
		if err != nil {
			return total, err
		}
		if count == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}

func (provider *routerPublicLoaderProvider) putPart(ctx context.Context, request contracts.PublicLoaderPutPartRequest) (contracts.PublicLoaderUploadSession, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicLoaderUploadSession{}, err
	}
	loaderCtx, subject, err := provider.serviceIdentity(ctx)
	if err != nil {
		return contracts.PublicLoaderUploadSession{}, err
	}
	body, size, checksum, err := provider.staging.ReadStagedObject(ctx, subject, request.Part.ObjectID)
	if err != nil {
		return contracts.PublicLoaderUploadSession{}, mapLoaderPublicError(err)
	}
	defer body.Close()
	if size != request.Part.Size || checksum != request.Part.SHA256 {
		return contracts.PublicLoaderUploadSession{}, contracts.ErrPublicInvalid
	}
	session, err := provider.api.PutUploadPart(loaderCtx, request.SessionID, request.Part.Number, size, checksum, body)
	if err != nil {
		return contracts.PublicLoaderUploadSession{}, mapLoaderPublicError(err)
	}
	return publicLoaderUploadSession(session, []contracts.PublicLoaderStagedPart{request.Part}, 0, session.PartSize), nil
}

func (provider *routerPublicLoaderProvider) commitUpload(ctx context.Context, request contracts.PublicLoaderCommitRequest) (contracts.PublicLoaderFile, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicLoaderFile{}, err
	}
	loaderCtx, _, err := provider.serviceIdentity(ctx)
	if err != nil {
		return contracts.PublicLoaderFile{}, err
	}
	file, err := provider.api.CommitUpload(loaderCtx, request.SessionID)
	if err != nil {
		return contracts.PublicLoaderFile{}, mapLoaderPublicError(err)
	}
	provider.purgeStagedParts(ctx, request.Parts)
	return publicLoaderFile(file), nil
}

func (provider *routerPublicLoaderProvider) abortUpload(ctx context.Context, request contracts.PublicLoaderAbortRequest) (contracts.PublicEmptyResponse, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicEmptyResponse{}, err
	}
	loaderCtx, _, err := provider.serviceIdentity(ctx)
	if err != nil {
		return contracts.PublicEmptyResponse{}, err
	}
	if err := provider.api.AbortUpload(loaderCtx, request.SessionID); err != nil {
		return contracts.PublicEmptyResponse{}, mapLoaderPublicError(err)
	}
	provider.purgeStagedParts(ctx, request.Parts)
	return contracts.PublicEmptyResponse{}, nil
}

func (provider *routerPublicLoaderProvider) statFile(ctx context.Context, request contracts.PublicLoaderStatFileRequest) (contracts.PublicLoaderFile, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicLoaderFile{}, err
	}
	loaderCtx, _, err := provider.serviceIdentity(ctx)
	if err != nil {
		return contracts.PublicLoaderFile{}, err
	}
	file, err := provider.api.StatFile(loaderCtx, request.FileID)
	if err != nil {
		return contracts.PublicLoaderFile{}, mapLoaderPublicError(err)
	}
	return publicLoaderFile(file), nil
}

func (provider *routerPublicLoaderProvider) deleteFile(ctx context.Context, request contracts.PublicLoaderDeleteFileRequest) (contracts.PublicEmptyResponse, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicEmptyResponse{}, err
	}
	loaderCtx, _, err := provider.serviceIdentity(ctx)
	if err != nil {
		return contracts.PublicEmptyResponse{}, err
	}
	if err := provider.api.DeleteFile(loaderCtx, request.FileID, request.Revision); err != nil {
		return contracts.PublicEmptyResponse{}, mapLoaderPublicError(err)
	}
	return contracts.PublicEmptyResponse{}, nil
}

func (provider *routerPublicLoaderProvider) getFileSet(ctx context.Context, request contracts.PublicLoaderGetFileSetRequest) (contracts.PublicLoaderFileSet, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicLoaderFileSet{}, err
	}
	loaderCtx, _, err := provider.serviceIdentity(ctx)
	if err != nil {
		return contracts.PublicLoaderFileSet{}, err
	}
	set, err := provider.api.GetFileSet(loaderCtx, request.FileSetID)
	if err != nil {
		return contracts.PublicLoaderFileSet{}, mapLoaderPublicError(err)
	}
	return publicLoaderFileSet(set), nil
}

func (provider *routerPublicLoaderProvider) addFileToSet(ctx context.Context, request contracts.PublicLoaderAddFileSetRequest) (contracts.PublicLoaderFileSet, error) {
	if err := provider.authenticate(ctx); err != nil {
		return contracts.PublicLoaderFileSet{}, err
	}
	loaderCtx, _, err := provider.serviceIdentity(ctx)
	if err != nil {
		return contracts.PublicLoaderFileSet{}, err
	}
	set, err := provider.api.AddFileToSet(loaderCtx, request.FileSetID, request.FileID, request.Revision)
	if err != nil {
		return contracts.PublicLoaderFileSet{}, mapLoaderPublicError(err)
	}
	return publicLoaderFileSet(set), nil
}

func publicLoaderUploadSession(session loaderapi.UploadSession, staged []contracts.PublicLoaderStagedPart, total, partSize int64) contracts.PublicLoaderUploadSession {
	expected := expectedRouterPublicParts(total, partSize)
	if expected == 0 && len(session.Parts) > 0 {
		expected = len(session.Parts)
	}
	if expected == 0 && len(staged) > 0 {
		expected = len(staged)
	}
	return contracts.PublicLoaderUploadSession{
		ID: session.ID, State: session.State, PartSize: session.PartSize,
		ExpectedParts: expected, StagedParts: append([]contracts.PublicLoaderStagedPart(nil), staged...),
	}
}

func publicLoaderFile(file loaderapi.File) contracts.PublicLoaderFile {
	return contracts.PublicLoaderFile{
		ID: file.ID, Name: file.Name, ContentType: file.ContentType, Size: file.Size,
		SHA256: file.SHA256, Revision: file.Revision, Groups: append([]string(nil), file.Groups...),
		Attributes: cloneRouterPublicStrings(file.Attributes),
	}
}

func publicLoaderFileSet(set loaderapi.FileSet) contracts.PublicLoaderFileSet {
	return contracts.PublicLoaderFileSet{
		ID: set.ID, Namespace: set.Namespace, Name: set.Name, ManagementGroup: set.ManagementGroup,
		Revision: set.Revision, CreatedAt: set.CreatedAt, UpdatedAt: set.UpdatedAt,
	}
}

func cloneRouterPublicStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func expectedRouterPublicParts(total, partSize int64) int {
	if total <= 0 || partSize <= 0 {
		return 0
	}
	return int((total + partSize - 1) / partSize)
}

func validRouterPublicUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func mapLoaderPublicError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, loaderapi.ErrUnauthorized):
		return contracts.ErrPublicUnauthorized
	case errors.Is(err, loaderapi.ErrForbidden):
		return contracts.ErrPublicForbidden
	case errors.Is(err, loaderapi.ErrNotFound):
		return contracts.ErrPublicNotFound
	case errors.Is(err, loaderapi.ErrConflict):
		return contracts.ErrPublicConflict
	case errors.Is(err, loaderapi.ErrInvalid):
		return contracts.ErrPublicInvalid
	case errors.Is(err, loaderapi.ErrTooLarge):
		return contracts.ErrPublicTooLarge
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return contracts.ErrPublicUnavailable
	}
}

func mapRouterPublicAccessError(err error) error {
	switch {
	case errors.Is(err, contracts.ErrAccessForbidden):
		return contracts.ErrPublicForbidden
	case errors.Is(err, contracts.ErrAccessUnauthorized), errors.Is(err, contracts.ErrAccessInvalid):
		return contracts.ErrPublicUnauthorized
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return contracts.ErrPublicUnavailable
	}
}

func mapRouterPublicStreamError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, contracts.ErrStreamUnavailable), errors.Is(err, contracts.ErrDispatcherUnavailable),
		errors.Is(err, bus.ErrStreamConsumed), errors.Is(err, bus.ErrStreamClosed):
		return contracts.ErrStreamUnavailable
	default:
		return contracts.ErrPublicUnavailable
	}
}
