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
var (
	publicationWorkflowService atomic.Pointer[publication.Service]
	publicationHandlerCore     atomic.Pointer[coreComponent]
	publicationHandlerRouter   atomic.Pointer[routerEngineComponent]
)

func setPublicationWorkflowService(service *publication.Service) {
	publicationWorkflowService.Store(service)
}

func setPublicationHandlerCore(component *coreComponent) {
	publicationHandlerCore.Store(component)
}

func setPublicationHandlerRouter(router *routerEngineComponent) {
	publicationHandlerRouter.Store(router)
}

func currentPublicationHandlerCore() *coreComponent { return publicationHandlerCore.Load() }

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
	if err := service.BeginBuild(ctx, payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	result, err := handler.build(ctx, payload)
	if err != nil {
		_ = service.FailTarget(ctx, payload.RunID, payload.RouteID, payload.Locale, err)
		return contracts.OperationOutcome{}, err
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
	website, err := handler.websiteContext(ctx)
	if err != nil {
		return result, err
	}
	digest, err := handler.inputDigest(website, payload)
	if err != nil {
		return result, err
	}
	result.InputDigest = digest

	job, err := website.service.service.BuildSnapshot(website.ctx, websiteapi.BuildSnapshotRequest{
		SnapshotID:     payload.PageSnapshotID,
		Locale:         payload.Locale,
		IdempotencyKey: contracts.PublicationBuildIdempotencyKey(payload),
	})
	if err != nil || job.ID == "" {
		if err == nil {
			err = publication.ErrUnavailable
		}
		return result, fmt.Errorf("publication build: %w", err)
	}
	ready, err := handler.awaitBuild(website, job.ID)
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

type authenticatedWebsite struct {
	ctx     context.Context
	service *websiteEngineComponent
}

func (handler publicationBuildHandler) websiteContext(ctx context.Context) (authenticatedWebsite, error) {
	if handler.component == nil || handler.component.config.Publication.WebsiteCredential == "" {
		return authenticatedWebsite{}, publication.ErrUnavailable
	}
	website := handler.component.websiteHandle.Load()
	if website == nil || website.service == nil || website.localeCatalog == nil || website.inputResolver == nil {
		return authenticatedWebsite{}, publication.ErrUnavailable
	}
	authCtx, err := website.service.Authenticate(ctx, handler.component.config.Publication.WebsiteCredential)
	if err != nil {
		return authenticatedWebsite{}, publication.ErrUnavailable
	}
	return authenticatedWebsite{ctx: authCtx, service: website}, nil
}

func (handler publicationBuildHandler) inputDigest(website authenticatedWebsite, payload contracts.PublicationBuildPayload) (string, error) {
	snapshot, err := website.service.service.GetSnapshot(website.ctx, payload.PageSnapshotID)
	if err != nil || snapshot.ID != payload.PageSnapshotID || snapshot.State != "ready" {
		if err == nil {
			err = publication.ErrInvalid
		}
		return "", fmt.Errorf("publication snapshot: %w", err)
	}
	revision, err := website.service.localeCatalog.LocaleCatalogRevision(website.ctx, payload.Locale)
	if err != nil || revision < 1 {
		return "", publication.ErrUnavailable
	}
	input, err := website.service.inputResolver.ResolveBuildInput(website.ctx, snapshot, payload.Locale, revision)
	if err != nil || prerendercontract.ValidateTrustedInput(input) != nil ||
		input.SnapshotID != payload.PageSnapshotID || input.Locale != payload.Locale ||
		input.ContentPublicationRevision != payload.ContentPublicationRevision ||
		input.SystemModelRevision != payload.SystemModelRevision {
		return "", fmt.Errorf("publication trusted input: %w", publication.ErrInvalid)
	}
	digest, err := prerendercontract.InputDigest(input)
	if err != nil {
		return "", fmt.Errorf("publication trusted input: %w", publication.ErrInvalid)
	}
	return digest, nil
}

func (handler publicationBuildHandler) awaitBuild(website authenticatedWebsite, buildID string) (websiteapi.BuildJob, error) {
	deadline := time.Now().Add(handler.component.config.Publication.BuildTimeout)
	for {
		job, err := website.service.service.GetBuild(website.ctx, buildID)
		if err != nil {
			return websiteapi.BuildJob{}, fmt.Errorf("publication build status: %w", err)
		}
		if job.State == "ready" || job.State == "failed" {
			return job, nil
		}
		if time.Now().After(deadline) {
			return websiteapi.BuildJob{}, fmt.Errorf("publication build: %w", context.DeadlineExceeded)
		}
		select {
		case <-website.ctx.Done():
			return websiteapi.BuildJob{}, website.ctx.Err()
		case <-time.After(handler.component.config.Publication.PollInterval):
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
	return contracts.NewCoreFilesClient(bus.NewModuleDispatcher(handler.component.dispatcher, "main.publication"), func() contracts.StreamRegistrar {
		return bus.NewScopedStreams()
	})
}

type publicationActivateHandler struct{ component *coreComponent }

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
	if err := service.BeginActivation(ctx, payload); err != nil {
		return contracts.OperationOutcome{}, err
	}
	result, err := handler.activate(ctx, payload, service)
	if err != nil {
		_ = service.FailTarget(ctx, payload.RunID, payload.RouteID, payload.Locale, err)
		return contracts.OperationOutcome{}, err
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
	if handler.component == nil || handler.component.config.Publication.WebsiteCredential == "" ||
		handler.component.config.Publication.RouterCredential == "" {
		return result, publication.ErrUnavailable
	}
	website := handler.component.websiteHandle.Load()
	if website == nil || website.service == nil || website.localeCatalog == nil || website.inputResolver == nil ||
		publicationHandlerRouter.Load() == nil || publicationHandlerRouter.Load().api == nil {
		return result, publication.ErrUnavailable
	}
	pinned, err := service.ArtifactDigest(ctx, payload.RunID, payload.RouteID, payload.Locale)
	if err != nil {
		return result, err
	}
	if pinned == "" {
		return result, publication.ErrStale
	}
	websiteCtx, err := website.service.Authenticate(ctx, handler.component.config.Publication.WebsiteCredential)
	if err != nil {
		return result, publication.ErrUnavailable
	}
	snapshotID, err := service.TargetSnapshot(ctx, payload.RunID, payload.RouteID, payload.Locale)
	if err != nil {
		return result, err
	}
	snapshot, err := website.service.GetSnapshot(websiteCtx, snapshotID)
	if err != nil || snapshot.ID != snapshotID || snapshot.State != "ready" {
		if err == nil {
			err = publication.ErrInvalid
		}
		return result, fmt.Errorf("publication snapshot: %w", err)
	}
	revision, err := website.localeCatalog.LocaleCatalogRevision(websiteCtx, payload.Locale)
	if err != nil || revision < 1 {
		return result, publication.ErrUnavailable
	}
	input, err := website.inputResolver.ResolveBuildInput(websiteCtx, snapshot, payload.Locale, revision)
	if err != nil {
		return result, fmt.Errorf("publication trusted input: %w", publication.ErrInvalid)
	}
	current, err := prerendercontract.InputDigest(input)
	if err != nil || current != pinned {
		return result, publication.ErrStale
	}
	routerCtx, err := publicationHandlerRouter.Load().api.Authenticate(ctx, handler.component.config.Publication.RouterCredential)
	if err != nil {
		return result, publication.ErrUnavailable
	}
	route, err := publicationHandlerRouter.Load().api.GetRoute(routerCtx, payload.RouteID)
	if err != nil {
		return result, fmt.Errorf("publication route read: %w", err)
	}
	slot, found := route.Target.LocaleTarget(payload.Locale)
	if !found || slot.Revision < 1 {
		return result, fmt.Errorf("publication route locale: %w", publication.ErrInvalid)
	}
	updated, err := publicationHandlerRouter.Load().api.ReplaceLocaleTarget(routerCtx, routerapi.ReplaceLocaleTargetRequest{
		RouteID: payload.RouteID, Locale: payload.Locale, ArtifactManifestFileID: payload.ArtifactManifestFileID,
		ExpectedRevision: slot.Revision, IdempotencyKey: contracts.PublicationActivateIdempotencyKey(payload),
	})
	if err != nil {
		if errors.Is(err, routerapi.ErrConflict) {
			return result, publication.ErrConflict
		}
		return result, fmt.Errorf("publication router cas: %w", err)
	}
	if updated.ID != payload.RouteID || updated.Revision <= slot.Revision {
		return result, publication.ErrInvalid
	}
	result.Revision = updated.Revision
	return result, nil
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
	result, err := service.FinalizeRun(ctx, payload.RunID)
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
