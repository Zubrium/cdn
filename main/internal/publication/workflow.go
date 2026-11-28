package publication

import (
	"context"
	"encoding/json"
	"fmt"

	workflow "github.com/Zubrium/cdn/pkg/contracts"
)

// PublishStartConfig is the complete, server-owned configuration of the root
// publication task. No request can change its operation or version.
type PublishStartConfig struct {
	MaxAttempts int
}

// PublishStartHandler validates the durable payload and declares class-scoped
// children. It never calls Website, Router or Core and cannot select a module.
type PublishStartHandler struct {
	config PublishStartConfig
}

func NewPublishStartHandler(config PublishStartConfig) (*PublishStartHandler, error) {
	if config.MaxAttempts < 1 || config.MaxAttempts > 100 {
		return nil, ErrUnavailable
	}
	return &PublishStartHandler{config: config}, nil
}

func (handler *PublishStartHandler) WorkerHandler(_ context.Context, task workflow.OperationTask) (workflow.OperationOutcome, error) {
	if handler == nil {
		return workflow.OperationOutcome{}, ErrUnavailable
	}
	payload, err := workflow.DecodePayload[workflow.PublicationWorkflowPayload](task.Payload)
	if err != nil {
		return workflow.OperationOutcome{}, fmt.Errorf("%w: malformed root payload", ErrInvalid)
	}
	if err := workflow.ValidatePublicationWorkflowPayload(payload); err != nil {
		return workflow.OperationOutcome{}, err
	}

	children := make([]workflow.OperationTaskChild, 0, 2*len(payload.Targets)+1)
	result := workflow.PublicationStartResult{
		Format: workflow.PublicationWorkflowFormat, RunID: payload.RunID,
		BuildEmissionKeys:    make([]string, 0, len(payload.Targets)),
		ActivateEmissionKeys: make([]string, 0, len(payload.Targets)),
		FinalizeEmissionKey:  workflow.PublicationFinalizeEmissionKey(payload.RunID),
	}
	for _, target := range payload.Targets {
		var dependencies []string
		if !target.SkipBuild {
			buildPayload, err := json.Marshal(workflow.PublicationBuildPayload{
				Attempt: payload.Attempt, RunID: payload.RunID, RouteID: target.RouteID, PageSnapshotID: target.PageSnapshotID,
				Locale: target.Locale, ContentPublicationRevision: payload.ContentPublicationRevision,
				SystemModelRevision: payload.SystemModelRevision, IdempotencyKey: payload.IdempotencyKey,
			})
			if err != nil {
				return workflow.OperationOutcome{}, err
			}
			buildEmission := workflow.PublicationBuildEmissionKey(payload.RunID, target.RouteID)
			children = append(children, workflow.OperationTaskChild{
				OperationID: workflow.PublicationBuildOperationID, Version: workflow.PublicationChildContractVersion,
				EmissionKey: buildEmission, Payload: buildPayload, MaxAttempts: handler.config.MaxAttempts,
			})
			result.BuildEmissionKeys = append(result.BuildEmissionKeys, buildEmission)

			dependencies = []string{buildEmission}
		}

		activatePayload, err := json.Marshal(workflow.PublicationActivatePayload{
			Attempt: payload.Attempt, CASAttempt: target.CASAttempt, RunID: payload.RunID, RouteID: target.RouteID, Locale: target.Locale,
			ArtifactManifestFileID: workflow.PublicationArtifactFileID(payload.RunID, target.RouteID, target.Locale),
			IdempotencyKey:         payload.IdempotencyKey,
		})
		if err != nil {
			return workflow.OperationOutcome{}, err
		}
		activateEmission := workflow.PublicationActivateEmissionKey(payload.RunID, target.RouteID)
		children = append(children, workflow.OperationTaskChild{
			OperationID: workflow.PublicationActivateOperationID, Version: workflow.PublicationChildContractVersion,
			EmissionKey: activateEmission, Payload: activatePayload, MaxAttempts: handler.config.MaxAttempts,
			DependsOnEmissions: dependencies,
		})
		result.ActivateEmissionKeys = append(result.ActivateEmissionKeys, activateEmission)
	}

	finalizePayload, err := json.Marshal(workflow.PublicationFinalizePayload{RunID: payload.RunID, Attempt: payload.Attempt})
	if err != nil {
		return workflow.OperationOutcome{}, err
	}
	children = append(children, workflow.OperationTaskChild{
		OperationID: workflow.PublicationFinalizeOperationID, Version: workflow.PublicationChildContractVersion,
		EmissionKey: result.FinalizeEmissionKey, Payload: finalizePayload, MaxAttempts: handler.config.MaxAttempts,
		DependsOnEmissions: append([]string(nil), result.ActivateEmissionKeys...),
	})
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return workflow.OperationOutcome{}, err
	}
	return workflow.OperationOutcome{Result: resultJSON, Children: children}, nil
}
