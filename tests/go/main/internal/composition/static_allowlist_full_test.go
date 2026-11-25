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
		contracts.PublishedContentReadOperation().Key,
		contracts.PublicationPublishOperation().Key,
		contracts.RetirePageOperation().Key,
		contracts.GetPageRetirementOperation().Key,
	}...)
}
