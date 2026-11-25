package composition

import (
	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/main/internal/contentbridge"
	"github.com/Zubrium/cdn/pkg/contracts"
)

const contentBridgeTaskClassWebsite = "engine.website"
const contentBridgeTaskClassContent = "engine.content"

// registerContentBridgeWorkerHandlers binds every root and child to exactly
// one compiled class. The roots are neutral validators; the children are
// narrow main-owned adapters to a single engine capability. No handler can
// dispatch an operation owned by another engine.
func registerContentBridgeWorkerHandlers(
	register func(classID, operationID string, version uint32, enabled bool, handler bus.TaskOperationHandler) error,
	selected map[string]struct{}, config appconfig.Resolved, core *coreComponent,
) error {
	enabled := config.Scales.WorkerPools.Publication.Enabled
	handlerConfig := contentbridge.Config{MaxAttempts: config.Coordinator.MaxAttempts}
	var workflows contentbridge.WorkflowClient = newContentBridgeWorkflowClient(nil)
	if core != nil {
		workflows = newContentBridgeWorkflowClient(core.dispatcher)
	}

	if _, compiled := selected[contentBridgeTaskClassContent]; compiled {
		projection, err := contentbridge.NewProjectionStartHandler(handlerConfig)
		if err != nil {
			return err
		}
		if err := register(contentBridgeTaskClassContent, contracts.ContentProjectionOperationID,
			contracts.ContentBridgeRootVersion, enabled, projection.WorkerHandler); err != nil {
			return err
		}
		if err := register(contentBridgeTaskClassContent, contracts.ContentBuildResolveOperationID,
			contracts.ContentBridgeChildVersion, enabled, contentBuildWorker{core: core, workflows: workflows}.WorkerHandler); err != nil {
			return err
		}
		if err := register(contentBridgeTaskClassContent, contracts.ContentLocaleReadOperationID,
			contracts.ContentBridgeChildVersion, enabled, contentLocaleWorker{core: core}.WorkerHandler); err != nil {
			return err
		}
	}
	if _, compiled := selected[contentBridgeTaskClassWebsite]; compiled {
		build, err := contentbridge.NewBuildStartHandler(handlerConfig)
		if err != nil {
			return err
		}
		locale, err := contentbridge.NewLocaleStartHandler(handlerConfig)
		if err != nil {
			return err
		}
		if err := register(contentBridgeTaskClassWebsite, contracts.ContentBuildOperationID,
			contracts.ContentBridgeRootVersion, enabled, build.WorkerHandler); err != nil {
			return err
		}
		if err := register(contentBridgeTaskClassWebsite, contracts.ContentLocaleOperationID,
			contracts.ContentBridgeRootVersion, enabled, locale.WorkerHandler); err != nil {
			return err
		}
		if err := register(contentBridgeTaskClassWebsite, contracts.WebsiteProjectionOperationID,
			contracts.ContentBridgeChildVersion, enabled, websiteProjectionWorker{core: core}.WorkerHandler); err != nil {
			return err
		}
		if err := register(contentBridgeTaskClassWebsite, contracts.ContentBuildProjectionOperationID,
			contracts.ContentBridgeChildVersion, enabled, websiteProjectionWorker{core: core}.WorkerHandler); err != nil {
			return err
		}
	}
	return nil
}
