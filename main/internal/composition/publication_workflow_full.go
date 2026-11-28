//go:build full && !devfixtures && !router

package composition

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	routerapi "github.com/Zubrium/cdn/engines/router/presentation"
	featureruntime "github.com/Zubrium/cdn/engines/website/features/runtime"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/main/internal/publication"
	"github.com/Zubrium/cdn/pkg/contracts"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
)

// The domain service and Core component are installed by the trusted
// publication component. Engine handles and credentials stay in the child
// handlers here; the domain service sees neither.
var publicationWorkflowService atomic.Pointer[publication.Service]

func setPublicationWorkflowService(service *publication.Service) {
	publicationWorkflowService.Store(service)
}

type publicationWorkflowStarter struct{ dispatcher contracts.Dispatcher }

func (starter publicationWorkflowStarter) Start(ctx context.Context, input contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	if starter.dispatcher == nil {
		return contracts.WorkflowStartReceipt{}, contracts.ErrWorkflowUnavailable
	}
	return contracts.CallWorkflowStart(ctx, starter.dispatcher, input)
}

func newPublicationRootHandler(maxAttempts int) (bus.TaskOperationHandler, error) {
	handler, err := publication.NewPublishStartHandler(publication.PublishStartConfig{MaxAttempts: maxAttempts})
	if err != nil {
		return nil, err
	}
	return handler.WorkerHandler, nil
}

type publicationBuildHandler struct{ component *coreComponent }

func (handler publicationBuildHandler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	payload, err := contracts.DecodePayload[contracts.PublicationBuildPayload](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if err := contracts.ValidatePublicationBuildPayload(payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	service := publicationWorkflowService.Load()
	if service == nil {
		return contracts.OperationOutcome{}, publication.ErrUnavailable
	}
	runTarget, err := service.ShouldRunTarget(ctx, payload.RunID, payload.RouteID, payload.Locale, payload.Attempt)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if !runTarget {
		return contracts.OperationOutcome{Result: json.RawMessage(`{"state":"unchanged"}`)}, nil
	}
	if err := service.BeginBuild(ctx, payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	result, err := handler.build(ctx, payload)
	if err != nil {
		// Store the domain failure so Core can continue sibling tasks.
		if recordErr := service.FailTarget(ctx, payload.RunID, payload.RouteID, payload.Locale, payload.Attempt, err); recordErr != nil {
			return contracts.OperationOutcome{}, recordErr
		}
		return contracts.OperationOutcome{Result: json.RawMessage(`{"state":"failed"}`)}, nil
	}
	if err := service.FinishBuild(ctx, payload, result); err != nil {
		return contracts.OperationOutcome{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	return contracts.OperationOutcome{Result: encoded}, nil
}

func (handler publicationBuildHandler) build(ctx context.Context, payload contracts.PublicationBuildPayload) (contracts.PublicationBuildResult, error) {
	result := contracts.PublicationBuildResult{
		Format: contracts.PublicationBuildResultFormat, RunID: payload.RunID, RouteID: payload.RouteID,
		Locale: payload.Locale, SnapshotID: payload.PageSnapshotID,
	}
	if handler.component == nil || handler.component.dispatcher == nil {
		return result, publication.ErrUnavailable
	}
	dispatcher := bus.NewModuleDispatcher(handler.component.dispatcher, publicationCaller)
	inputRequest := contracts.PublicationInputRequest{
		SnapshotID: payload.PageSnapshotID, Locale: payload.Locale, ContentRevision: payload.ContentPublicationRevision,
	}
	input, err := contracts.Call(ctx, dispatcher, contracts.PublicationInputOperation(), inputRequest)
	if err != nil {
		return result, err
	}
	if input.SystemModelRevision != payload.SystemModelRevision {
		return result, publication.ErrStale
	}
	digest := input.Digest
	result.InputDigest = digest
	job, err := contracts.Call(ctx, dispatcher, contracts.PublicationWebsiteBuildStartOperation(), contracts.PublicationWebsiteBuildRequest{
		PublicationInputRequest: inputRequest, IdempotencyKey: contracts.PublicationBuildIdempotencyKey(payload),
	})
	if err != nil || job.ID == "" {
		if err == nil {
			err = publication.ErrUnavailable
		}
		return result, fmt.Errorf("publication build: %w", err)
	}
	ready, err := awaitBuildStatus(ctx, func(ctx context.Context, id string) (websiteapi.BuildJob, error) {
		return contracts.Call(ctx, dispatcher, contracts.PublicationWebsiteBuildReadOperation(), contracts.OrchestrationObjectRequest{ID: id})
	}, job.ID, handler.component.config.Publication.BuildTimeout, handler.component.config.Publication.PollInterval)
	if err != nil {
		return result, err
	}
	if ready.State == "failed" || ready.ErrorCode != "" || ready.ArtifactManifestFileID == "" {
		code := ready.ErrorCode
		if code == "" {
			code = "not_ready"
		}
		return result, fmt.Errorf("publication build: %s", code)
	}
	if ready.SnapshotID != payload.PageSnapshotID || ready.Locale != payload.Locale ||
		ready.InputDigest != digest || job.InputDigest != digest {
		return result, publication.ErrStale
	}
	artifactID, err := handler.packageArtifact(ctx, payload, ready.ArtifactManifestFileID, digest)
	if err != nil {
		return result, err
	}
	result.ArtifactManifestFileID = artifactID
	return result, nil
}

// awaitBuildStatus drives one durable build to a terminal state. Concurrent
// readers legitimately race on the build record's CAS revision; a conflict
// means another reader advanced it, so reload instead of failing the target.
func awaitBuildStatus(ctx context.Context, getBuild func(context.Context, string) (websiteapi.BuildJob, error),
	buildID string, timeout, pollInterval time.Duration) (websiteapi.BuildJob, error) {
	deadline := time.Now().Add(timeout)
	for {
		job, err := getBuild(ctx, buildID)
		if err != nil {
			if !errors.Is(err, websiteapi.ErrConflict) {
				return websiteapi.BuildJob{}, fmt.Errorf("publication build status for %s: %w", buildID, err)
			}
		} else if job.State == "ready" || job.State == "failed" {
			return job, nil
		}
		if time.Now().After(deadline) {
			return websiteapi.BuildJob{}, fmt.Errorf("publication build: %w", context.DeadlineExceeded)
		}
		select {
		case <-ctx.Done():
			return websiteapi.BuildJob{}, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// packageArtifact converts the verified prerender manifest into the static
// artifact consumed by Router. Both the source read and packaged write go
// through module-scoped Core file capabilities; no build result selects the
// route, locale, group or file identity.
func (handler publicationBuildHandler) packageArtifact(ctx context.Context, payload contracts.PublicationBuildPayload, sourceFileID, digest string) (string, error) {
	files, err := handler.coreFiles(ctx)
	if err != nil {
		return "", err
	}
	reader, _, err := files.ReadFile(ctx, sourceFileID)
	if err != nil {
		return "", fmt.Errorf("publication artifact read: %w", err)
	}
	defer reader.Close()
	limit := int64(handler.component.config.Core.ModuleData.MaxPayloadBytes)
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit {
		return "", fmt.Errorf("publication artifact read: %w", publication.ErrInvalid)
	}
	source, err := prerendercontract.VerifyManifest(data, digest)
	if err != nil || source.Locale != payload.Locale || len(source.Artifacts) == 0 {
		return "", publication.ErrStale
	}
	manifest := featureruntime.ArtifactManifest{Format: "static-page-artifact/v1", Assets: map[string]featureruntime.ArtifactFile{}}
	for _, artifact := range source.Artifacts {
		entry := featureruntime.ArtifactFile{FileID: artifact.FileID, ContentType: artifact.MediaType,
			SHA256: artifact.SHA256, Size: artifact.Size}
		if artifact.FileID == "" || artifact.SHA256 == "" || artifact.Size < 1 {
			return "", publication.ErrInvalid
		}
		if artifact.Path == "index.html" {
			if manifest.Document.FileID != "" {
				return "", publication.ErrInvalid
			}
			manifest.Document = entry
			continue
		}
		if artifact.Path == "" || strings.Contains(artifact.Path, "..") {
			return "", publication.ErrInvalid
		}
		manifest.Assets[artifact.Path] = entry
	}
	if manifest.Document.FileID == "" {
		return "", publication.ErrInvalid
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	manifest.Digest, err = featureruntime.ArtifactDigest(encoded)
	if err != nil {
		return "", fmt.Errorf("publication artifact digest: %w", err)
	}
	encoded, err = json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	fileID, err := files.CreateFileVerified(ctx, contracts.PublicationArtifactFileID(payload.RunID, payload.RouteID, payload.Locale),
		[]string{"features"},
		map[string]string{"artifact_path": "manifest.json", "record_type": "publication_artifact"},
		"application/json", int64(len(encoded)), hex.EncodeToString(sum[:]), bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("publication artifact create: %w", err)
	}
	return fileID, nil
}

func (handler publicationBuildHandler) coreFiles(ctx context.Context) (contracts.CoreFiles, error) {
	if handler.component == nil || handler.component.dispatcher == nil {
		return nil, publication.ErrUnavailable
	}
	return contracts.NewCoreFilesClient(bus.NewModuleDispatcher(handler.component.dispatcher, publicationCaller), func() contracts.StreamRegistrar {
		return bus.NewScopedStreams()
	})
}

type publicationActivateHandler struct{ component *coreComponent }

// builtArtifactResolver is the server-owned activation precondition: the
// deterministic upload request key emitted by the workflow root is replaced
// with the file ID Core actually issued to the completed build.
type builtArtifactResolver interface {
	BuiltArtifactFileID(ctx context.Context, runID, routeID, locale string) (string, error)
}

func publicationActivationPayloadWithBuiltArtifact(ctx context.Context, resolver builtArtifactResolver, payload contracts.PublicationActivatePayload) (contracts.PublicationActivatePayload, error) {
	if resolver == nil {
		return payload, publication.ErrUnavailable
	}
	artifactID, err := resolver.BuiltArtifactFileID(ctx, payload.RunID, payload.RouteID, payload.Locale)
	if err != nil {
		return payload, err
	}
	payload.ArtifactManifestFileID = artifactID
	return payload, nil
}

func (handler publicationActivateHandler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	payload, err := contracts.DecodePayload[contracts.PublicationActivatePayload](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if err := contracts.ValidatePublicationActivatePayload(payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	service := publicationWorkflowService.Load()
	if service == nil {
		return contracts.OperationOutcome{}, publication.ErrUnavailable
	}
	runTarget, err := service.ShouldRunTarget(ctx, payload.RunID, payload.RouteID, payload.Locale, payload.Attempt)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if !runTarget {
		return contracts.OperationOutcome{Result: json.RawMessage(`{"state":"unchanged"}`)}, nil
	}
	payload, err = publicationActivationPayloadWithBuiltArtifact(ctx, service, payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if err := service.BeginActivation(ctx, payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	result, err := handler.activate(ctx, payload, service)
	if err != nil {
		// Store the domain failure so Core can continue sibling tasks.
		if recordErr := service.FailTarget(ctx, payload.RunID, payload.RouteID, payload.Locale, payload.Attempt, err); recordErr != nil {
			return contracts.OperationOutcome{}, recordErr
		}
		return contracts.OperationOutcome{Result: json.RawMessage(`{"state":"failed"}`)}, nil
	}
	if err := service.FinishActivation(ctx, payload, result); err != nil {
		return contracts.OperationOutcome{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	return contracts.OperationOutcome{Result: encoded}, nil
}

func (handler publicationActivateHandler) activate(ctx context.Context, payload contracts.PublicationActivatePayload, service *publication.Service) (contracts.PublicationActivateResult, error) {
	result := contracts.PublicationActivateResult{
		Format: contracts.PublicationActivateResultFormat, RunID: payload.RunID, RouteID: payload.RouteID,
		Locale: payload.Locale, ArtifactManifestFileID: payload.ArtifactManifestFileID,
	}
	if handler.component == nil || handler.component.dispatcher == nil {
		return result, publication.ErrUnavailable
	}
	dispatcher := bus.NewModuleDispatcher(handler.component.dispatcher, publicationCaller)
	pinned, err := service.ArtifactDigest(ctx, payload.RunID, payload.RouteID, payload.Locale)
	if err != nil {
		return result, err
	}
	snapshotID, contentRevision, err := service.TargetContentRevision(ctx, payload.RunID, payload.RouteID, payload.Locale)
	if err != nil {
		return result, err
	}
	input, err := contracts.Call(ctx, dispatcher, contracts.PublicationInputOperation(), contracts.PublicationInputRequest{
		SnapshotID: snapshotID, Locale: payload.Locale, ContentRevision: contentRevision,
	})
	if err != nil {
		return result, err
	}
	if pinned == "" || input.Digest != pinned {
		return result, publication.ErrStale
	}
	route, err := contracts.Call(ctx, dispatcher, contracts.OrchestrationRouteReadOperation(), contracts.OrchestrationRouteRequest{RouteID: payload.RouteID, Locale: payload.Locale})
	if err != nil {
		return result, fmt.Errorf("publication route read: %w", err)
	}
	slot := route.Slot
	if route.ID != payload.RouteID || slot == nil || slot.Locale != payload.Locale || slot.Revision < 1 {
		return result, fmt.Errorf("publication route locale: %w", publication.ErrInvalid)
	}
	expectedRevision, err := service.ReserveActivationRevision(ctx, payload, slot.Revision)
	if err != nil {
		return result, err
	}
	updated, err := contracts.Call(ctx, dispatcher, contracts.PublicationRouteCASOperation(), contracts.PublicationRouteCASRequest{
		ReplaceLocaleTargetRequest: contracts.ReplaceLocaleTargetRequest{
			RouteID: payload.RouteID, Locale: payload.Locale, ArtifactManifestFileID: payload.ArtifactManifestFileID,
			ExpectedRevision: expectedRevision,
		},
		IdempotencyKey: contracts.PublicationActivateIdempotencyKey(payload),
	})
	if err != nil {
		if errors.Is(err, routerapi.ErrConflict) {
			return result, publication.ErrConflict
		}
		return result, fmt.Errorf("publication router cas: %w", err)
	}
	activatedRevision, err := publicationActivatedRevision(updated, payload, expectedRevision)
	if err != nil {
		return result, err
	}
	result.Revision = activatedRevision
	return result, nil
}

func publicationActivatedRevision(route contracts.OrchestrationRouteState, payload contracts.PublicationActivatePayload, expected int64) (int64, error) {
	slot := route.Slot
	if route.ID != payload.RouteID || slot == nil || slot.Locale != payload.Locale || slot.Revision != expected+1 || slot.ArtifactManifestFileID != payload.ArtifactManifestFileID {
		return 0, publication.ErrStale
	}
	return slot.Revision, nil
}

type publicationFinalizeHandler struct{}

func (publicationFinalizeHandler) WorkerHandler(ctx context.Context, task contracts.OperationTask) (contracts.OperationOutcome, error) {
	payload, err := contracts.DecodePayload[contracts.PublicationFinalizePayload](task.Payload)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	if err := contracts.ValidatePublicationFinalizePayload(payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	service := publicationWorkflowService.Load()
	if service == nil {
		return contracts.OperationOutcome{}, publication.ErrUnavailable
	}
	result, err := service.FinalizeRun(ctx, payload.RunID, payload.Attempt)
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	encoded, err := json.Marshal(contracts.PublicationFinalizeResult{
		Format: contracts.PublicationFinalizeResultFormat, RunID: payload.RunID, State: result.State,
	})
	if err != nil {
		return contracts.OperationOutcome{}, err
	}
	return contracts.OperationOutcome{Result: encoded}, nil
}
