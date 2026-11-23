package bus

import (
	"context"
	"io"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type VerifiedFileCreatorResolver func(context.Context, string) (contracts.VerifiedFileCreator, error)
type CoreFileReaderResolver func(context.Context, string) (contracts.CoreFileReader, error)
type CoreFileStatResolver func(context.Context, string, contracts.AuthorizationSubject) (contracts.CoreFileStat, error)
type CoreFileDeleteResolver func(context.Context, string) (contracts.CoreFileDeleter, error)
type CoreFilesResolver func(context.Context, string) (contracts.CoreFiles, error)

// RegisterFileCreateVerified registers the only Core file operation needed by
// the prerender worker. The caller identity selects a prebound capability;
// neither owner nor session identity is accepted from the JSON payload.
func RegisterFileCreateVerified(dispatcher *Dispatcher, resolve VerifiedFileCreatorResolver) error {
	if dispatcher == nil || resolve == nil {
		return contracts.ErrInvalidOperation
	}
	return Register(dispatcher, contracts.FileCreateVerifiedOperation(), func(ctx context.Context, request contracts.FileCreateVerifiedRequest) (contracts.FileCreateResponse, error) {
		moduleID := ModuleIDFromContext(ctx)
		if moduleID == "" {
			return contracts.FileCreateResponse{}, ErrInvalidModuleIdentity
		}
		creator, err := resolve(ctx, moduleID)
		if err != nil {
			return contracts.FileCreateResponse{}, err
		}
		if creator == nil {
			return contracts.FileCreateResponse{}, contracts.ErrDispatcherUnavailable
		}
		streams, ok := contracts.StreamCapabilityFromContext(ctx)
		if !ok {
			return contracts.FileCreateResponse{}, contracts.ErrStreamUnavailable
		}
		body, err := streams.OpenRead(ctx, request.Body)
		if err != nil {
			return contracts.FileCreateResponse{}, err
		}
		defer body.Close()
		fileID, err := creator.CreateFileVerified(ctx, request.RequestKey, request.Groups, request.Metadata,
			request.ContentType, request.Size, request.SHA256, io.Reader(body))
		return contracts.FileCreateResponse{FileID: fileID}, err
	})
}

// RegisterFileRead streams file bytes through the caller-owned scoped stream.
// The producer lease remains active until the body is consumed or explicitly
// closed, so dispatcher shutdown drains outstanding file reads.
func RegisterFileRead(dispatcher *Dispatcher, resolve CoreFileReaderResolver) error {
	if dispatcher == nil || resolve == nil {
		return contracts.ErrInvalidOperation
	}
	return Register(dispatcher, contracts.FileReadOperation(), func(ctx context.Context, request contracts.FileReadRequest) (contracts.FileReadResponse, error) {
		moduleID := ModuleIDFromContext(ctx)
		if moduleID == "" {
			return contracts.FileReadResponse{}, ErrInvalidModuleIdentity
		}
		readerCapability, err := resolve(ctx, moduleID)
		if err != nil {
			return contracts.FileReadResponse{}, err
		}
		if readerCapability == nil {
			return contracts.FileReadResponse{}, contracts.ErrDispatcherUnavailable
		}
		streamCapability, ok := contracts.StreamCapabilityFromContext(ctx)
		if !ok {
			return contracts.FileReadResponse{}, contracts.ErrStreamUnavailable
		}
		writer, err := streamCapability.OpenWrite(ctx, request.Body)
		if err != nil {
			return contracts.FileReadResponse{}, err
		}
		body, file, err := readerCapability.ReadFile(ctx, request.FileID)
		if err != nil {
			_ = writer.Close()
			return contracts.FileReadResponse{}, err
		}
		retainer, ok := streamCapability.(contracts.StreamRetainer)
		if !ok {
			_ = body.Close()
			_ = writer.Close()
			return contracts.FileReadResponse{}, contracts.ErrStreamUnavailable
		}
		release, err := retainer.Retain()
		if err != nil {
			_ = body.Close()
			_ = writer.Close()
			return contracts.FileReadResponse{}, err
		}
		go func() {
			defer release()
			defer body.Close()
			_, copyErr := io.Copy(writer, body)
			if closer, ok := writer.(interface{ CloseWithError(error) error }); ok && copyErr != nil {
				_ = closer.CloseWithError(copyErr)
				return
			}
			_ = writer.Close()
		}()
		return contracts.FileReadResponse{File: file}, nil
	})
}

func RegisterFileStat(dispatcher *Dispatcher, resolve CoreFileStatResolver) error {
	if dispatcher == nil || resolve == nil {
		return contracts.ErrInvalidOperation
	}
	return Register(dispatcher, contracts.FileStatOperation(), func(ctx context.Context, request contracts.FileStatRequest) (contracts.FileStatResponse, error) {
		moduleID := ModuleIDFromContext(ctx)
		if moduleID == "" {
			return contracts.FileStatResponse{}, ErrInvalidModuleIdentity
		}
		capability, err := resolve(ctx, moduleID, request.Subject)
		if err != nil {
			return contracts.FileStatResponse{}, err
		}
		if capability == nil {
			return contracts.FileStatResponse{}, contracts.ErrDispatcherUnavailable
		}
		file, err := capability.StatFile(ctx, request.FileID)
		return contracts.FileStatResponse{File: file}, err
	})
}

func RegisterFileDelete(dispatcher *Dispatcher, resolve CoreFileDeleteResolver) error {
	if dispatcher == nil || resolve == nil {
		return contracts.ErrInvalidOperation
	}
	return Register(dispatcher, contracts.FileDeleteOperation(), func(ctx context.Context, request contracts.FileDeleteRequest) (contracts.FileDeleteResponse, error) {
		moduleID := ModuleIDFromContext(ctx)
		if moduleID == "" {
			return contracts.FileDeleteResponse{}, ErrInvalidModuleIdentity
		}
		capability, err := resolve(ctx, moduleID)
		if err != nil {
			return contracts.FileDeleteResponse{}, err
		}
		if capability == nil {
			return contracts.FileDeleteResponse{}, contracts.ErrDispatcherUnavailable
		}
		return contracts.FileDeleteResponse{}, capability.DeleteFile(ctx, request.FileID, request.ExpectedRevision)
	})
}

func RegisterFileSetOperations(dispatcher *Dispatcher, resolve CoreFilesResolver) error {
	if dispatcher == nil || resolve == nil {
		return contracts.ErrInvalidOperation
	}
	getFiles := func(ctx context.Context) (contracts.CoreFiles, error) {
		moduleID := ModuleIDFromContext(ctx)
		if moduleID == "" {
			return nil, ErrInvalidModuleIdentity
		}
		files, err := resolve(ctx, moduleID)
		if err != nil {
			return nil, err
		}
		if files == nil {
			return nil, contracts.ErrDispatcherUnavailable
		}
		return files, nil
	}
	if err := Register(dispatcher, contracts.FileSetGetOperation(), func(ctx context.Context, request contracts.FileSetGetRequest) (contracts.FileSetGetResponse, error) {
		files, err := getFiles(ctx)
		if err != nil {
			return contracts.FileSetGetResponse{}, err
		}
		set, err := files.GetManagedFileSet(ctx, request.SetID)
		return contracts.FileSetGetResponse{Set: set}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.FileSetListOperation(), func(ctx context.Context, request contracts.FileSetListRequest) (contracts.FileSetListResponse, error) {
		files, err := getFiles(ctx)
		if err != nil {
			return contracts.FileSetListResponse{}, err
		}
		sets, cursor, err := files.ListManagedFileSets(ctx, request.Namespace, request.Query, request.Cursor, request.Limit)
		return contracts.FileSetListResponse{Sets: sets, NextCursor: cursor}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.FileSetMembersOperation(), func(ctx context.Context, request contracts.FileSetMembersRequest) (contracts.FileSetMembersResponse, error) {
		files, err := getFiles(ctx)
		if err != nil {
			return contracts.FileSetMembersResponse{}, err
		}
		members, cursor, err := files.ListFileSet(ctx, request.SetID, request.Cursor, request.Limit)
		return contracts.FileSetMembersResponse{Files: members, NextCursor: cursor}, err
	}); err != nil {
		return err
	}
	if err := Register(dispatcher, contracts.FileSetAddOperation(), func(ctx context.Context, request contracts.FileSetMutationRequest) (contracts.FileSetMutationResponse, error) {
		files, err := getFiles(ctx)
		if err != nil {
			return contracts.FileSetMutationResponse{}, err
		}
		set, err := files.AddFileToManagedSet(ctx, request.SetID, request.FileID, request.ExpectedRevision)
		return contracts.FileSetMutationResponse{Set: set}, err
	}); err != nil {
		return err
	}
	return Register(dispatcher, contracts.FileSetRemoveOperation(), func(ctx context.Context, request contracts.FileSetMutationRequest) (contracts.FileSetMutationResponse, error) {
		files, err := getFiles(ctx)
		if err != nil {
			return contracts.FileSetMutationResponse{}, err
		}
		set, err := files.RemoveFileFromManagedSet(ctx, request.SetID, request.FileID, request.ExpectedRevision)
		return contracts.FileSetMutationResponse{Set: set}, err
	})
}
