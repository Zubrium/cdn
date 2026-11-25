// Package contentbridge owns the neutral Content<->Website workflow roots. A
// root validates only the server-owned payload and declares fixed child tasks;
// it never calls an engine, holds an engine handle, or selects a task class.
// The class-to-child binding is compiled statically in main.
package contentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	ErrUnavailable = errors.New("content bridge capability is unavailable")
	ErrInvalid     = errors.New("content bridge task is invalid")
)

type Config struct{ MaxAttempts int }

func validateConfig(config Config) error {
	if config.MaxAttempts < 1 || config.MaxAttempts > 100 {
		return ErrUnavailable
	}
	return nil
}

type ProjectionStartHandler struct{ config Config }

func NewProjectionStartHandler(config Config) (*ProjectionStartHandler, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &ProjectionStartHandler{config: config}, nil
}

func (h *ProjectionStartHandler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	if h == nil || ctx == nil || task.TaskID == "" {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return contracts.OperationOutcome{}, err
	}
	payload, err := contracts.DecodePayload[contracts.ContentProjectionWorkflowPayload](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, fmt.Errorf("%w: malformed projection payload", ErrInvalid)
	}
	if err := contracts.ValidateContentProjectionWorkflowPayload(payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	emission := contracts.ContentProjectionEmissionKey(payload.PageSnapshotID, payload.Locale)
	if !contracts.ValidContentBridgeEmissionKey(emission) {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	child, err := json.Marshal(contracts.ContentProjectionRequest{
		PageSnapshotID: payload.PageSnapshotID, Locale: payload.Locale,
		AllowProvisioning: payload.AllowProvisioning,
	})
	if err != nil {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	result, err := json.Marshal(contracts.ContentBridgeRootResult{
		Format: contracts.ContentProjectionWorkflowFormat, EmissionKey: emission,
	})
	if err != nil {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	return contracts.OperationOutcome{
		Result: result,
		Children: []contracts.OperationTaskChild{{
			OperationID: contracts.WebsiteProjectionOperationID, Version: contracts.ContentBridgeChildVersion,
			EmissionKey: emission, Payload: child, MaxAttempts: h.config.MaxAttempts,
		}},
	}, nil
}

type BuildStartHandler struct{ config Config }

func NewBuildStartHandler(config Config) (*BuildStartHandler, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &BuildStartHandler{config: config}, nil
}

func (h *BuildStartHandler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	if h == nil || ctx == nil || task.TaskID == "" {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return contracts.OperationOutcome{}, err
	}
	payload, err := contracts.DecodePayload[contracts.ContentBuildWorkflowPayload](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, fmt.Errorf("%w: malformed build payload", ErrInvalid)
	}
	if err := contracts.ValidateBuildWorkflowPayload(payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	emission := contracts.ContentBuildEmissionKey(payload.PageSnapshotID, payload.Locale)
	projectionEmission := contracts.ContentBuildProjectionEmissionKey(payload.PageSnapshotID, payload.Locale)
	if !contracts.ValidContentBridgeEmissionKey(emission) || !contracts.ValidContentBridgeEmissionKey(projectionEmission) {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	projectionPayload, err := json.Marshal(contracts.ContentProjectionRequest{
		PageSnapshotID: payload.PageSnapshotID, Locale: payload.Locale,
	})
	if err != nil {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	child, err := json.Marshal(contracts.ContentBuildProjectionRequest{
		PageSnapshotID: payload.PageSnapshotID, Locale: payload.Locale,
	})
	if err != nil {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	result, err := json.Marshal(contracts.ContentBridgeRootResult{
		Format: contracts.ContentBuildWorkflowFormat, EmissionKey: emission, ProjectionEmissionKey: projectionEmission,
	})
	if err != nil {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	return contracts.OperationOutcome{
		Result: result,
		Children: []contracts.OperationTaskChild{
			{
				OperationID: contracts.ContentBuildProjectionOperationID, Version: contracts.ContentBridgeChildVersion,
				EmissionKey: projectionEmission, Payload: projectionPayload, MaxAttempts: h.config.MaxAttempts,
			},
			{
				OperationID: contracts.ContentBuildResolveOperationID, Version: contracts.ContentBridgeChildVersion,
				EmissionKey: emission, Payload: child, MaxAttempts: h.config.MaxAttempts,
				DependsOnEmissions: []string{projectionEmission},
			},
		},
	}, nil
}

type LocaleStartHandler struct{ config Config }

func NewLocaleStartHandler(config Config) (*LocaleStartHandler, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &LocaleStartHandler{config: config}, nil
}

func (h *LocaleStartHandler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	if h == nil || ctx == nil || task.TaskID == "" {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return contracts.OperationOutcome{}, err
	}
	payload, err := contracts.DecodePayload[contracts.ContentLocaleWorkflowPayload](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, fmt.Errorf("%w: malformed locale payload", ErrInvalid)
	}
	if err := contracts.ValidateLocaleWorkflowPayload(payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	emission := contracts.ContentLocaleEmissionKey
	if !contracts.ValidContentBridgeEmissionKey(emission) {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	child, err := json.Marshal(contracts.ContentLocaleCatalogRequest{})
	if err != nil {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	result, err := json.Marshal(contracts.ContentBridgeRootResult{
		Format: contracts.ContentLocaleWorkflowFormat, EmissionKey: emission,
	})
	if err != nil {
		return contracts.OperationOutcome{}, ErrInvalid
	}
	return contracts.OperationOutcome{
		Result: result,
		Children: []contracts.OperationTaskChild{{
			OperationID: contracts.ContentLocaleReadOperationID, Version: contracts.ContentBridgeChildVersion,
			EmissionKey: emission, Payload: child, MaxAttempts: h.config.MaxAttempts,
		}},
	}, nil
}
