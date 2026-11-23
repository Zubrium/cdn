package contracts

import (
	"context"
	"errors"
	"regexp"
)

var (
	ErrAccessInvalid      = errors.New("invalid access operation")
	ErrAccessUnauthorized = errors.New("access is unauthorized")
	ErrAccessForbidden    = errors.New("access is forbidden")
	ErrAccessUnavailable  = errors.New("access service unavailable")
)

type AccessLevel uint8

const (
	AccessLevelDenied AccessLevel = iota
	AccessLevelRead
	AccessLevelWrite
)

type Principal struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	Active                  bool   `json:"active"`
	CanManageAdministrators bool   `json:"can_manage_administrators"`
}

// AuthorizationSubject carries the credential or browser session token used
// for one internal Core operation. It is never part of an HTTP response.
type AuthorizationSubject struct {
	Credential   string `json:"credential,omitempty"`
	SessionToken string `json:"session_token,omitempty"`
}

func (subject AuthorizationSubject) Validate() error {
	if (subject.Credential == "") == (subject.SessionToken == "") {
		return ErrAccessInvalid
	}
	return nil
}

func ValidAccessGroupKey(value string) bool { return accessGroupKeyPattern.MatchString(value) }

type AccessAuthenticateRequest struct {
	Credential string `json:"credential"`
}

type AccessAuthenticateResponse struct {
	Principal Principal `json:"principal"`
}

type AccessAuthenticateSessionRequest struct {
	Token string `json:"token"`
}

type AccessAuthenticateSessionResponse struct {
	Principal Principal `json:"principal"`
}

type AccessRequireGroupRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	Groups  []string             `json:"groups"`
	Level   AccessLevel          `json:"level"`
}

type AccessRequireGroupResponse struct{}

type CoreAccess interface {
	Authenticate(context.Context, string) (Principal, error)
	AuthenticateSession(context.Context, string) (Principal, error)
	RequireGroupLevel(context.Context, AuthorizationSubject, []string, AccessLevel) error
}

func AccessAuthenticateOperation() Operation[AccessAuthenticateRequest, AccessAuthenticateResponse] {
	return Operation[AccessAuthenticateRequest, AccessAuthenticateResponse]{
		Key: Key{ID: "core.access.authenticate", Version: 1},
		Validate: func(request AccessAuthenticateRequest) error {
			if request.Credential == "" {
				return ErrAccessInvalid
			}
			return nil
		},
	}
}

func AccessAuthenticateSessionOperation() Operation[AccessAuthenticateSessionRequest, AccessAuthenticateSessionResponse] {
	return Operation[AccessAuthenticateSessionRequest, AccessAuthenticateSessionResponse]{
		Key: Key{ID: "core.access.authenticate-session", Version: 1},
		Validate: func(request AccessAuthenticateSessionRequest) error {
			if request.Token == "" {
				return ErrAccessInvalid
			}
			return nil
		},
	}
}

var accessGroupKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func AccessRequireGroupOperation() Operation[AccessRequireGroupRequest, AccessRequireGroupResponse] {
	return Operation[AccessRequireGroupRequest, AccessRequireGroupResponse]{
		Key: Key{ID: "core.access.require-group", Version: 1},
		Validate: func(request AccessRequireGroupRequest) error {
			if request.Subject.Validate() != nil || len(request.Groups) == 0 || len(request.Groups) > 32 ||
				(request.Level != AccessLevelRead && request.Level != AccessLevelWrite) {
				return ErrAccessInvalid
			}
			seen := make(map[string]struct{}, len(request.Groups))
			for _, group := range request.Groups {
				if !accessGroupKeyPattern.MatchString(group) {
					return ErrAccessInvalid
				}
				if _, exists := seen[group]; exists {
					return ErrAccessInvalid
				}
				seen[group] = struct{}{}
			}
			return nil
		},
	}
}

type coreAccessClient struct{ dispatcher Dispatcher }

func NewCoreAccessClient(dispatcher Dispatcher) (CoreAccess, error) {
	if dispatcher == nil {
		return nil, ErrAccessUnavailable
	}
	return &coreAccessClient{dispatcher: dispatcher}, nil
}

func (client *coreAccessClient) Authenticate(ctx context.Context, credential string) (Principal, error) {
	response, err := Call(ctx, client.dispatcher, AccessAuthenticateOperation(), AccessAuthenticateRequest{Credential: credential})
	return response.Principal, err
}

func (client *coreAccessClient) AuthenticateSession(ctx context.Context, token string) (Principal, error) {
	response, err := Call(ctx, client.dispatcher, AccessAuthenticateSessionOperation(), AccessAuthenticateSessionRequest{Token: token})
	return response.Principal, err
}

func (client *coreAccessClient) RequireGroupLevel(ctx context.Context, subject AuthorizationSubject, groups []string, level AccessLevel) error {
	_, err := Call(ctx, client.dispatcher, AccessRequireGroupOperation(), AccessRequireGroupRequest{
		Subject: subject, Groups: groups, Level: level,
	})
	return err
}

var _ CoreAccess = (*coreAccessClient)(nil)
