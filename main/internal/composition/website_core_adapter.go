package composition

import (
	"context"
	"io"

	platform "github.com/Zubrium/cdn/core/presentation"
	websiteplatform "github.com/Zubrium/cdn/engines/website/application/platform"
	"github.com/Zubrium/cdn/pkg/contracts"
)

type websiteAuthority struct{ runtime *platform.Runtime }

func (a websiteAuthority) Authenticate(ctx context.Context, credential string) (websiteplatform.Session, websiteplatform.Principal, error) {
	_, principal, err := a.runtime.Identity().Authenticate(ctx, credential)
	return contracts.AuthorizationSubject{Credential: credential}, contractPrincipal(principal), mapCoreAccessError(err)
}

func (a websiteAuthority) RequireGroupLevel(ctx context.Context, subject websiteplatform.Session, groups []string, level websiteplatform.AccessLevel) error {
	session, err := resolveCoreSession(ctx, a.runtime, subject)
	if err != nil {
		return mapCoreAccessError(err)
	}
	coreLevel := platform.AccessLevelRead
	if level == contracts.AccessLevelWrite {
		coreLevel = platform.AccessLevelWrite
	}
	return mapCoreAccessError(a.runtime.GroupAuthorization().RequireGroupLevel(ctx, session, groups, coreLevel))
}

type websiteSessions struct{ runtime *platform.Runtime }

func (a websiteSessions) AuthenticateSession(ctx context.Context, token string) (websiteplatform.Session, websiteplatform.Principal, error) {
	_, principal, err := a.runtime.Sessions().AuthenticateSession(ctx, token)
	return contracts.AuthorizationSubject{SessionToken: token}, contractPrincipal(principal), mapCoreAccessError(err)
}

type websiteFiles struct{ runtime *platform.Runtime }

func (a websiteFiles) session(ctx context.Context, subject websiteplatform.Session) (platform.Session, error) {
	return resolveCoreSession(ctx, a.runtime, subject)
}

func (a websiteFiles) ReadFile(ctx context.Context, subject websiteplatform.Session, id string) (io.ReadCloser, websiteplatform.File, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, contracts.File{}, mapCoreFileError(err)
	}
	body, file, err := a.runtime.Files().ReadFile(ctx, session, id)
	return body, contractFile(file), mapCoreFileError(err)
}

func (a websiteFiles) StatFile(ctx context.Context, subject websiteplatform.Session, id string) (websiteplatform.File, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return contracts.File{}, mapCoreFileError(err)
	}
	file, err := a.runtime.Files().StatFile(ctx, session, id)
	return contractFile(file), mapCoreFileError(err)
}

func (a websiteFiles) CreateFileVerified(ctx context.Context, subject websiteplatform.Session, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return "", mapCoreFileError(err)
	}
	id, err := a.runtime.Files().CreateFileVerified(ctx, session, requestKey, groups, metadata, contentType, size, checksum, body)
	return id, mapCoreFileError(err)
}

func (a websiteFiles) ListManagedFileSets(ctx context.Context, subject websiteplatform.Session, namespace, query, cursor string, limit int) ([]websiteplatform.FileSet, string, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, "", mapCoreFileError(err)
	}
	sets, next, err := a.runtime.Files().ListManagedFileSets(ctx, session, namespace, query, cursor, limit)
	result := make([]contracts.FileSet, len(sets))
	for i, set := range sets {
		result[i] = contractFileSet(set)
	}
	return result, next, mapCoreFileError(err)
}

func (a websiteFiles) ListFileSet(ctx context.Context, subject websiteplatform.Session, setID, cursor string, limit int) ([]websiteplatform.File, string, error) {
	session, err := a.session(ctx, subject)
	if err != nil {
		return nil, "", mapCoreFileError(err)
	}
	files, next, err := a.runtime.Files().ListFileSet(ctx, session, setID, cursor, limit)
	result := make([]contracts.File, len(files))
	for i, file := range files {
		result[i] = contractFile(file)
	}
	return result, next, mapCoreFileError(err)
}

func resolveCoreSession(ctx context.Context, runtime *platform.Runtime, subject contracts.AuthorizationSubject) (platform.Session, error) {
	if runtime == nil || subject.Validate() != nil {
		return platform.Session{}, contracts.ErrAccessInvalid
	}
	if subject.Credential != "" {
		session, _, err := runtime.Identity().Authenticate(ctx, subject.Credential)
		return session, err
	}
	session, _, err := runtime.Sessions().AuthenticateSession(ctx, subject.SessionToken)
	return session, err
}
