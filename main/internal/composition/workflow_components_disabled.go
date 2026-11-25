//go:build !full || devfixtures || router

package composition

import (
	"context"
	"errors"

	"github.com/Zubrium/cdn/main/internal/publication"
	"github.com/Zubrium/cdn/main/internal/retirement"
	"github.com/Zubrium/cdn/pkg/contracts"
)

var errWorkflowUnavailableInProfile = errors.New("durable workflow is not available in this profile")

type publicationWorkflowStarter struct{ dispatcher contracts.Dispatcher }

func (publicationWorkflowStarter) Start(context.Context, contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	return contracts.WorkflowStartReceipt{}, errWorkflowUnavailableInProfile
}

func setPublicationWorkflowService(*publication.Service) {}
func setPublicationHandlerRouter(*routerEngineComponent) {}

type retirementWorkflowStarter struct{ dispatcher contracts.Dispatcher }

func (retirementWorkflowStarter) Start(context.Context, contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	return contracts.WorkflowStartReceipt{}, errWorkflowUnavailableInProfile
}

func setRetirementWorkflowService(*retirement.Service)  {}
func setRetirementHandlerRouter(*routerEngineComponent) {}

func markPageRetiring(context.Context, *coreComponent, retirement.Job) error {
	return errWorkflowUnavailableInProfile
}
