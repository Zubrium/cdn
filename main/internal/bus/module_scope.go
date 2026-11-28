package bus

import (
	"context"
	"errors"
	"regexp"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	ErrInvalidModuleIdentity        = errors.New("invalid DataBus module identity")
	ErrOperationNotAllowedForModule = errors.New("operation is not allowed for this DataBus module")
)

var moduleIdentityPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,127}$`)

type moduleIdentityContextKey struct{}

type moduleDispatcher struct {
	dispatcher contracts.Dispatcher
	moduleID   string
	allowed    map[contracts.Key]struct{}
}

// NewModuleDispatcher returns a dispatcher whose source identity is fixed by
// the composition root and attached outside the JSON operation payload. The
// identity also narrows the operation set: a module can dispatch only the
// operations declared for it in allowedKeysForModule. The Core Coordinator is
// the bus owner and is exempt from this narrowing.
func NewModuleDispatcher(dispatcher contracts.Dispatcher, moduleID string) contracts.Dispatcher {
	if moduleID == "core.coordinator" && dispatcher != nil {
		return moduleDispatcher{dispatcher: dispatcher, moduleID: moduleID}
	}
	return newModuleDispatcher(dispatcher, moduleID, allowedKeysForModule(moduleID))
}

// NewRecordOwnerDispatcher binds a registered record descriptor owner to the
// record operations. The owner ID selects the scoped store in Core.
func NewRecordOwnerDispatcher(dispatcher contracts.Dispatcher, ownerID string) contracts.Dispatcher {
	return newModuleDispatcher(dispatcher, ownerID, recordOperationKeys())
}

func newModuleDispatcher(dispatcher contracts.Dispatcher, moduleID string, keys []contracts.Key) contracts.Dispatcher {
	if dispatcher == nil || !moduleIdentityPattern.MatchString(moduleID) {
		return nil
	}
	allowed := make(map[contracts.Key]struct{})
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	return moduleDispatcher{dispatcher: dispatcher, moduleID: moduleID, allowed: allowed}
}

func (dispatcher moduleDispatcher) Dispatch(ctx context.Context, request contracts.Envelope) (contracts.Envelope, error) {
	if dispatcher.dispatcher == nil || !moduleIdentityPattern.MatchString(dispatcher.moduleID) {
		return contracts.Envelope{}, ErrInvalidModuleIdentity
	}
	key := request.Key()
	if dispatcher.allowed != nil {
		if _, allowed := dispatcher.allowed[key]; !allowed {
			return contracts.Envelope{}, ErrOperationNotAllowedForModule
		}
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

func recordOperationKeys() []contracts.Key {
	return []contracts.Key{
		contracts.RecordGetOperation().Key,
		contracts.RecordGetExpiredOperation().Key,
		contracts.RecordPutOperation().Key,
		contracts.RecordDeleteOperation().Key,
		contracts.RecordBatchOperation().Key,
		contracts.RecordListOperation().Key,
		contracts.RecordListExpiredOperation().Key,
	}
}

func allowedKeysForModule(moduleID string) []contracts.Key {
	core := []contracts.Key{contracts.CoreReadinessOperation().Key}
	access := []contracts.Key{
		contracts.AccessAuthenticateOperation().Key,
		contracts.AccessAuthenticateSessionOperation().Key,
		contracts.AccessRequireGroupOperation().Key,
	}
	files := []contracts.Key{
		contracts.FileCreateVerifiedOperation().Key,
		contracts.FileReadOperation().Key,
		contracts.FileStatOperation().Key,
		contracts.FileDeleteOperation().Key,
		contracts.FileSetGetOperation().Key,
		contracts.FileSetListOperation().Key,
		contracts.FileSetMembersOperation().Key,
		contracts.FileSetAddOperation().Key,
		contracts.FileSetRemoveOperation().Key,
	}
	records := recordOperationKeys()
	workflow := []contracts.Key{
		contracts.WorkflowStartOperation().Key,
		contracts.WorkflowStateOperation().Key,
		contracts.WorkflowTaskResultOperation().Key,
		contracts.WorkflowTaskIDResolveOperation().Key,
	}
	adminWorkflow := []contracts.Key{
		contracts.WorkflowListOperation().Key,
		contracts.WorkflowGetOperation().Key,
		contracts.WorkflowRetryOperation().Key,
	}
	publication := []contracts.Key{contracts.PublicationPublishOperation().Key}
	retirement := []contracts.Key{
		contracts.RetirePageOperation().Key,
		contracts.GetPageRetirementOperation().Key,
	}
	publishedContent := []contracts.Key{contracts.PublishedContentReadOperation().Key}
	adminIdentity := AdminIdentityOperationKeys()

	join := func(groups ...[]contracts.Key) []contracts.Key {
		var result []contracts.Key
		for _, group := range groups {
			result = append(result, group...)
		}
		return result
	}

	switch moduleID {
	case "engine.website":
		return join(core, access, files, records, workflow)
	case "engine.website.import":
		return join(core, access, files, records, workflow)
	case "engine.content":
		return join(core, access, files, records, workflow)
	case "engine.forms":
		return join(core, access, files, records)
	case "engine.loader":
		return join(core, access, files, records)
	case "engine.router":
		return join(core, access, files, publishedContent)
	case "worker.archive", "worker.media", "worker.prerender":
		return join(core, access, files, records, workflow)
	case "main.publication":
		return join(core, access, files, records, workflow, publication, []contracts.Key{
			contracts.ListContentPublicationIntentsOperation().Key,
			contracts.AcknowledgeContentPublicationIntentOperation().Key,
			contracts.OrchestrationRouteReadOperation().Key,
			contracts.PublicationInputOperation().Key,
			contracts.PublicationWebsiteBuildStartOperation().Key,
			contracts.PublicationWebsiteBuildReadOperation().Key,
			contracts.PublicationRouteCASOperation().Key,
		})
	case "composition.retirement":
		return join(core, access, files, records, workflow, retirement, []contracts.Key{
			contracts.OrchestrationRouteReadOperation().Key,
			contracts.RetirementRouteDeleteOperation().Key,
			contracts.RetirementWebsiteReadOperation().Key,
			contracts.RetirementWebsiteTransitionOperation().Key,
		})
	case "admin.identity":
		return join(core, access, adminIdentity)
	case "admin.coordinator":
		return join(core, access, workflow, adminWorkflow)
	case "admin.api":
		return join(core, access, files, records, workflow, adminWorkflow, adminIdentity, publication, retirement, publishedContent)
	default:
		return nil
	}
}
