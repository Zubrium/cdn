package bus

import (
	"context"
	"errors"
	"regexp"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var ErrInvalidModuleIdentity = errors.New("invalid DataBus module identity")

var moduleIdentityPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,127}$`)

type moduleIdentityContextKey struct{}

type moduleDispatcher struct {
	dispatcher contracts.Dispatcher
	moduleID   string
}

// NewModuleDispatcher returns a dispatcher whose source identity is fixed by
// the composition root and attached outside the JSON operation payload.
func NewModuleDispatcher(dispatcher contracts.Dispatcher, moduleID string) contracts.Dispatcher {
	if dispatcher == nil || !moduleIdentityPattern.MatchString(moduleID) {
		return nil
	}
	return moduleDispatcher{dispatcher: dispatcher, moduleID: moduleID}
}

func (dispatcher moduleDispatcher) Dispatch(ctx context.Context, request contracts.Envelope) (contracts.Envelope, error) {
	if dispatcher.dispatcher == nil || !moduleIdentityPattern.MatchString(dispatcher.moduleID) {
		return contracts.Envelope{}, ErrInvalidModuleIdentity
	}
	return dispatcher.dispatcher.Dispatch(context.WithValue(ctx, moduleIdentityContextKey{}, dispatcher.moduleID), request)
}

// ModuleIDFromContext returns the identity attached by NewModuleDispatcher.
// Core-operation handlers use this value to select a pre-issued scoped
// capability; request payloads cannot select or override it.
func ModuleIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	moduleID, _ := ctx.Value(moduleIdentityContextKey{}).(string)
	if !moduleIdentityPattern.MatchString(moduleID) {
		return ""
	}
	return moduleID
}
