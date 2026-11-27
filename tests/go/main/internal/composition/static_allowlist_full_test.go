//go:build full && !devfixtures && !router

package composition

import (
	"testing"

	websiteworkflow "github.com/Zubrium/cdn/engines/website/workflow"
	"github.com/Zubrium/cdn/pkg/contracts"
	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/Zubrium/cdn/workers/media"
	"github.com/Zubrium/cdn/workers/prerender"
)

func TestFullProfileStaticAllowlistIsExact(t *testing.T) {
	assertCompiledDispatcherAllowlist(t, BuildPlan(), []contracts.Key{
		{ID: archive.OperationID, Version: 1},
		{ID: media.OperationID, Version: 1},
		{ID: media.OperationID, Version: mediaprofile.WorkContractVersion},
		{ID: prerender.OperationID, Version: 1},
		{ID: websiteworkflow.OperationID, Version: 1},
		{ID: websiteworkflow.MediaOperationID, Version: 1},
		{ID: websiteworkflow.ForkOperationID, Version: 1},
		{ID: websiteworkflow.ForkCopyOperationID, Version: websiteworkflow.ForkChildContractVersion},
		{ID: websiteworkflow.ForkFinalizeOperationID, Version: websiteworkflow.ForkChildContractVersion},
		contracts.PublishedContentReadOperation().Key,
		contracts.PublicationPublishOperation().Key,
		contracts.ListContentPublicationIntentsOperation().Key,
		contracts.AcknowledgeContentPublicationIntentOperation().Key,
		contracts.RetirePageOperation().Key,
		contracts.GetPageRetirementOperation().Key,
		{ID: contracts.ContentProjectionOperationID, Version: contracts.ContentBridgeRootVersion},
		{ID: contracts.WebsiteProjectionOperationID, Version: contracts.ContentBridgeChildVersion},
		{ID: contracts.ContentBuildProjectionOperationID, Version: contracts.ContentBridgeChildVersion},
		{ID: contracts.ContentBuildOperationID, Version: contracts.ContentBridgeRootVersion},
		{ID: contracts.ContentBuildResolveOperationID, Version: contracts.ContentBridgeChildVersion},
		{ID: contracts.ContentLocaleOperationID, Version: contracts.ContentBridgeRootVersion},
		{ID: contracts.ContentLocaleReadOperationID, Version: contracts.ContentBridgeChildVersion},
	}...)
}
