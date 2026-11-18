package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

var componentIDPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
var operationIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,127}$`)

var ErrWorkflowOperationUnavailable = errors.New("workflow operation is not registered")

type WorkflowStore = corecoord.WorkflowRepository

type WorkflowStartRequest struct {
	OperationID     string
	ContractVersion int
	IdempotencyKey  string
	Payload         json.RawMessage
}

type WorkflowReceipt struct {
	WorkflowID uuid.UUID
	RootTaskID uuid.UUID
	State      string
	Created    bool
}

// WorkflowCatalog is an immutable set of component contracts validated
// before Core starts accepting traffic.
type WorkflowCatalog struct {
	components map[string]map[string]struct{}
}

func NewWorkflowCatalog(descriptors []corecoord.ComponentDescriptor) (*WorkflowCatalog, error) {
	if len(descriptors) == 0 {
		return nil, ErrInvalid
	}
	components := make(map[string]map[string]struct{}, len(descriptors))
	dependencies := make(map[string][]string, len(descriptors))
	for _, descriptor := range descriptors {
		if !componentIDPattern.MatchString(descriptor.ID) || descriptor.Version < 1 || descriptor.Version > 1<<16 ||
			(descriptor.Kind != "engine" && descriptor.Kind != "worker" && descriptor.Kind != "entrypoint") {
			return nil, ErrInvalid
		}
		if _, duplicate := components[descriptor.ID]; duplicate {
			return nil, ErrDuplicate
		}
		operations := make(map[string]struct{}, len(descriptor.Operations))
		for _, operation := range descriptor.Operations {
			if !operationIDPattern.MatchString(operation.ID) || operation.ContractVersion < 1 || operation.ContractVersion > 1<<16 {
				return nil, ErrInvalid
			}
			key := operation.ID + "\x00" + itoa(operation.ContractVersion)
			if _, duplicate := operations[key]; duplicate {
				return nil, ErrDuplicate
			}
			operations[key] = struct{}{}
		}
		components[descriptor.ID] = operations
		dependencies[descriptor.ID] = append([]string(nil), descriptor.Dependencies...)
	}
	for id, required := range dependencies {
		seen := make(map[string]struct{}, len(required))
		for _, dependency := range required {
			if _, exists := components[dependency]; !exists || dependency == id {
				return nil, ErrInvalid
			}
			if _, duplicate := seen[dependency]; duplicate {
				return nil, ErrDuplicate
			}
			seen[dependency] = struct{}{}
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return false
		}
		if visited[id] {
			return true
		}
		visiting[id] = true
		for _, dependency := range dependencies[id] {
			if !visit(dependency) {
				return false
			}
		}
		delete(visiting, id)
		visited[id] = true
		return true
	}
	for id := range components {
		if !visit(id) {
			return nil, ErrInvalid
		}
	}
	return &WorkflowCatalog{components: components}, nil
}

func (c *WorkflowCatalog) Operations(componentID string) []string {
	if c == nil {
		return nil
	}
	operations := make([]string, 0)
	for key := range c.components[componentID] {
		operation, _, _ := strings.Cut(key, "\x00")
		operations = append(operations, operation)
	}
	sort.Strings(operations)
	return operations
}

type WorkflowClientConfig struct {
	Store         WorkflowStore
	Catalog       *WorkflowCatalog
	OwnerEngineID string
	InstanceID    uuid.UUID
	ConfigEpoch   int64
	MaxAttempts   int
	Retention     time.Duration
}

// WorkflowClient is scoped at construction to one registered component. It
// cannot submit work under another engine identity or an undeclared operation.
type WorkflowClient struct {
	store         WorkflowStore
	catalog       *WorkflowCatalog
	ownerEngineID string
	instanceID    uuid.UUID
	configEpoch   int64
	maxAttempts   int
	retention     time.Duration
}

func NewWorkflowClient(config WorkflowClientConfig) (*WorkflowClient, error) {
	if isNilInterface(config.Store) || config.Catalog == nil || !componentIDPattern.MatchString(config.OwnerEngineID) ||
		config.InstanceID == uuid.Nil || config.ConfigEpoch <= 0 || config.MaxAttempts < 1 || config.MaxAttempts > 100 ||
		config.Retention <= 0 || config.Retention > 365*24*time.Hour {
		return nil, ErrInvalid
	}
	if _, exists := config.Catalog.components[config.OwnerEngineID]; !exists {
		return nil, ErrComponentUnavailable
	}
	return &WorkflowClient{store: config.Store, catalog: config.Catalog, ownerEngineID: config.OwnerEngineID,
		instanceID: config.InstanceID, configEpoch: config.ConfigEpoch, maxAttempts: config.MaxAttempts,
		retention: config.Retention}, nil
}

func (c *WorkflowClient) Start(ctx context.Context, request WorkflowStartRequest) (WorkflowReceipt, error) {
	if c == nil || isNilInterface(c.store) || ctx == nil || request.ContractVersion < 1 || request.ContractVersion > 1<<16 ||
		!operationIDPattern.MatchString(request.OperationID) || !validCoordinatorIdempotencyKey(request.IdempotencyKey) ||
		!validWorkflowPayload(request.Payload) {
		return WorkflowReceipt{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return WorkflowReceipt{}, err
	}
	if _, exists := c.catalog.components[c.ownerEngineID][request.OperationID+"\x00"+itoa(request.ContractVersion)]; !exists {
		return WorkflowReceipt{}, ErrWorkflowOperationUnavailable
	}
	workflowID, err := uuid.NewRandom()
	if err != nil {
		return WorkflowReceipt{}, err
	}
	rootID, err := uuid.NewRandom()
	if err != nil {
		return WorkflowReceipt{}, err
	}
	input := make([]byte, 0, len(request.Payload)+len(request.OperationID)+16)
	input = append(input, request.OperationID...)
	input = append(input, 0)
	input = append(input, itoa(request.ContractVersion)...)
	input = append(input, 0)
	input = append(input, request.Payload...)
	inputHash := sha256.Sum256(input)
	workflow, created, err := c.store.StartWorkflow(ctx, corecoord.WorkflowRequest{
		ID: workflowID, Owner: c.instanceID, OwnerEngineID: c.ownerEngineID, IdempotencyKey: request.IdempotencyKey,
		InputHash: inputHash[:], ConfigEpoch: c.configEpoch, Retention: c.retention,
		Root: corecoord.TaskRequest{ID: rootID, WorkflowID: workflowID, ComponentID: c.ownerEngineID,
			OperationID: request.OperationID, ContractVersion: request.ContractVersion, Payload: append(json.RawMessage(nil), request.Payload...),
			MaxAttempts: c.maxAttempts},
	})
	if err != nil {
		return WorkflowReceipt{}, err
	}
	if workflow.ID == uuid.Nil || workflow.RootTaskID == uuid.Nil || workflow.OwnerEngineID != c.ownerEngineID {
		return WorkflowReceipt{}, ErrInvalid
	}
	return WorkflowReceipt{WorkflowID: workflow.ID, RootTaskID: workflow.RootTaskID, State: workflow.State, Created: created}, nil
}

func (c *WorkflowClient) Get(ctx context.Context, id uuid.UUID) (corecoord.Workflow, error) {
	if c == nil || isNilInterface(c.store) || ctx == nil || id == uuid.Nil {
		return corecoord.Workflow{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return corecoord.Workflow{}, err
	}
	return c.store.GetWorkflow(ctx, c.ownerEngineID, c.instanceID, c.configEpoch, id)
}

func (c *WorkflowClient) Cancel(ctx context.Context, id uuid.UUID) error {
	if c == nil || isNilInterface(c.store) || ctx == nil || id == uuid.Nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.store.CancelWorkflow(ctx, c.ownerEngineID, c.instanceID, c.configEpoch, id, c.retention)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
