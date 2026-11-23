package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	platform "github.com/Zubrium/cdn/core/presentation"
	loaderapp "github.com/Zubrium/cdn/engines/loader/features/files/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
	"github.com/Zubrium/cdn/presentation/admin/base/transport"
	"github.com/Zubrium/cdn/presentation/router"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/Zubrium/cdn/workers/media"
	"github.com/Zubrium/cdn/workers/prerender"
	"github.com/google/uuid"
)

type coreComponent struct {
	config          appconfig.Resolved
	runtime         *platform.Runtime
	secret          string
	operations      map[contracts.Key]TaskOperationBinding
	selected        map[string]struct{}
	accessGroups    map[string][]string
	dispatcher      *bus.Dispatcher
	recordStoreMu   sync.RWMutex
	recordStores    map[string]contracts.RecordDataStore
	fileServiceMu   sync.RWMutex
	fileServices    map[string]contracts.CoreFiles
	archiveHandle   atomic.Pointer[archive.Handler]
	mediaHandle     atomic.Pointer[media.V2Handler]
	mediaSandbox    atomic.Pointer[media.DockerSandbox]
	prerenderHandle atomic.Pointer[prerender.DockerWorker]
	websiteHandle   atomic.Pointer[websiteEngineComponent]
	contentHandle   atomic.Pointer[contentEngineComponent]
}

func mergeProfileAccessGroups(configured map[string]platform.AccessLevel, descriptors []contracts.ComponentDescriptor) (map[string]platform.AccessLevel, error) {
	groups := make(map[string]platform.AccessLevel, len(configured))
	for key, level := range configured {
		groups[key] = level
	}
	for _, descriptor := range descriptors {
		for _, key := range descriptor.AccessGroups {
			if !platform.ValidAccessGroupKey(key) {
				return nil, fmt.Errorf("component %q declares invalid access group %q", descriptor.ID, key)
			}
			if _, configured := groups[key]; !configured {
				groups[key] = platform.AccessLevelWrite
			}
		}
	}
	return groups, nil
}

func (c *coreComponent) Start(ctx context.Context) error {
	profileRegistry := NewProfileRegistry(platform.NewRegistry())
	if err := RegisterProfile(profileRegistry); err != nil {
		return fmt.Errorf("register compiled profile: %w", err)
	}
	c.accessGroups = make(map[string][]string)
	for _, descriptor := range profileRegistry.ComponentDescriptors() {
		if moduleUsesCoreAccess(descriptor.ID, c.selected) && len(descriptor.AccessGroups) > 0 {
			c.accessGroups[descriptor.ID] = append([]string(nil), descriptor.AccessGroups...)
		}
	}
	coreConfig := c.config.Core
	groups, err := mergeProfileAccessGroups(coreConfig.AccessGroups, profileRegistry.ComponentDescriptors())
	if err != nil {
		return err
	}
	coreConfig.AccessGroups = groups
	c.config.Core.AccessGroups = groups
	runtime, secret, err := platform.Start(ctx, coreConfig)
	if err != nil {
		return err
	}
	c.runtime, c.secret = runtime, secret
	if err := RegisterProfile(NewProfileRegistry(runtime.Registry)); err != nil {
		_ = runtime.Close()
		return fmt.Errorf("register compiled profile: %w", err)
	}
	if err := runtime.Registry.Register("coordinator"); err != nil {
		_ = runtime.Close()
		return fmt.Errorf("register Coordinator capability: %w", err)
	}
	if c.config.FeatureImport.Enabled {
		if err := c.configureArchiveImport(ctx, runtime); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("configure archive import worker: %w", err)
		}
	}
	if c.config.Media.Enabled {
		if err := c.configureMedia(ctx, runtime); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("configure media worker: %w", err)
		}
	}
	if c.config.Prerender.Enabled {
		if err := c.configurePrerender(ctx, runtime); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("configure prerender worker: %w", err)
		}
	}
	settings := c.config.Coordinator
	if err := runtime.StartCoordinator(ctx, platform.CoordinatorRuntimeConfig{
		Fingerprint:   c.config.CoordinatorFingerprint,
		InstanceLease: settings.InstanceLease, HeartbeatInterval: settings.HeartbeatInterval,
		ExecutorConcurrency: settings.ExecutorConcurrency, TaskLease: settings.TaskLease,
		TaskTimeout: settings.TaskTimeout, PollInterval: settings.PollInterval,
		RetryBase: settings.RetryBase, RetryMaximum: settings.RetryMaximum,
		MaxAttempts: settings.MaxAttempts, MaxWorkflowTasks: settings.MaxWorkflowTasks,
		MaxChildrenPerTask: settings.MaxChildrenPerTask, WorkflowRetention: settings.WorkflowRetention,
		ShutdownTimeout: c.config.HTTP.ShutdownTimeout, TaskClasses: taskClasses(c.operations),
		OperationExecutor: NewDataBusTaskExecutor(c.dispatcher, c.operations),
	}); err != nil {
		_ = runtime.Close()
		return fmt.Errorf("start Core Coordinator: %w", err)
	}
	if c.dispatcher == nil {
		_ = runtime.Close()
		return errors.New("runtime bus is not configured")
	}
	if err := bus.RegisterCoreReadiness(c.dispatcher, runtime.CoordinatorStartupReady); err != nil {
		_ = runtime.Close()
		return fmt.Errorf("register Core readiness operation: %w", err)
	}
	if hasModuleDataConsumers(c.selected) {
		if err := bus.RegisterRecordDataStore(c.dispatcher, c.recordDataStore); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("register Core record operations: %w", err)
		}
	}
	if hasCoreFileConsumers(c.selected) {
		if err := bus.RegisterFileCreateVerified(c.dispatcher, c.coreFileCreator); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("register Core file creation operation: %w", err)
		}
		if err := bus.RegisterFileRead(c.dispatcher, c.coreFileReader); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("register Core file read operation: %w", err)
		}
		if err := bus.RegisterFileStat(c.dispatcher, c.coreFileStat); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("register Core file stat operation: %w", err)
		}
		if err := bus.RegisterFileDelete(c.dispatcher, c.coreFileDelete); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("register Core file delete operation: %w", err)
		}
		if err := bus.RegisterFileSetOperations(c.dispatcher, c.coreFiles); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("register Core FileSet operations: %w", err)
		}
	}
	if len(c.accessGroups) > 0 {
		if err := bus.RegisterCoreAccess(c.dispatcher, coreRuntimeAccess{runtime: runtime}, c.accessGroups); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("register Core access operations: %w", err)
		}
	}
	if _, selected := c.selected["admin.coordinator"]; selected {
		if _, err := runtime.NewWorkflowControl(); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("configure workflow control operation: %w", err)
		}
		if err := bus.RegisterWorkflowControl(c.dispatcher, adminWorkflowControl{core: c}); err != nil {
			_ = runtime.Close()
			return fmt.Errorf("register workflow control operations: %w", err)
		}
	}
	if err := c.dispatcher.Seal(); err != nil {
		_ = runtime.Close()
		return fmt.Errorf("seal runtime bus: %w", err)
	}
	return nil
}

// configureArchiveImport issues only narrow Core capabilities to the archive
// worker. SQL, object storage, module identity, and visitor credentials never
// cross this boundary; the worker receives one authenticated service session.
func (c *coreComponent) configureArchiveImport(ctx context.Context, runtime *platform.Runtime) error {
	settings := c.config.FeatureImport
	session, _, err := runtime.Identity().Authenticate(ctx, settings.ServiceCredential)
	if err != nil {
		return fmt.Errorf("authenticate feature import service: %w", err)
	}
	c.setCoreFiles(archive.DataModuleID, coreSessionFileAdapter{files: runtime.Files(), session: session})
	progress, err := c.registerModuleData(archive.DataDescriptor())
	if err != nil {
		return fmt.Errorf("register archive progress capability: %w", err)
	}
	fileClient, err := contracts.NewCoreFilesClient(bus.NewModuleDispatcher(c.dispatcher, archive.DataModuleID), func() contracts.StreamRegistrar {
		return bus.NewScopedStreams()
	})
	if err != nil {
		return err
	}
	handler, err := archive.NewHandler(fileClient, archive.Config{
		Limits:          settings.Limits,
		StagingDir:      settings.StagingDirectory,
		TargetFileSetID: settings.TargetFileSetID,
		OutputGroups:    settings.OutputGroups,
		Progress:        progress,
		Index:           fileClient,
	})
	if err != nil {
		return err
	}
	c.archiveHandle.Store(handler)
	return nil
}

// configureMedia issues only narrow Core capabilities to the supervised media
// worker. The OS sandbox is constructed through a real in-container preflight;
// a failed probe prevents worker registration instead of falling back.
func (c *coreComponent) configureMedia(ctx context.Context, runtime *platform.Runtime) error {
	settings := c.config.Media
	session, _, err := runtime.Identity().Authenticate(ctx, settings.ServiceCredential)
	if err != nil {
		return err
	}
	c.setCoreFiles(media.DataModuleID, coreSessionFileAdapter{files: runtime.Files(), session: session})
	progress, err := c.registerModuleData(media.DataDescriptor())
	if err != nil {
		return err
	}
	sandbox, err := media.NewDockerSandbox(ctx, media.DockerSandboxConfig{
		Image: settings.SandboxImage, DockerBinary: settings.DockerBinary, StagingRoot: settings.StagingDirectory,
		CPUs: settings.CPUs, MemoryLimitBytes: settings.MemoryLimitBytes, PIDsLimit: settings.PIDsLimit,
		MaxInputBytes: settings.MaxInputBytes, MaxOutputBytes: settings.MaxOutputBytes, Timeout: settings.Timeout,
	})
	if err != nil {
		return err
	}
	fileClient, err := contracts.NewCoreFilesClient(bus.NewModuleDispatcher(c.dispatcher, media.DataModuleID), func() contracts.StreamRegistrar {
		return bus.NewScopedStreams()
	})
	if err != nil {
		return err
	}
	handler, err := media.NewV2Handler(fileClient, progress, sandbox,
		settings.MaxConcurrency, settings.MaxInputBytes+settings.MaxOutputBytes)
	if err != nil {
		return err
	}
	c.mediaSandbox.Store(sandbox)
	c.mediaHandle.Store(handler)
	return nil
}

// configurePrerender issues only a Core file-writing capability to the
// sandboxed renderer. The container image, limits, and staging path are
// trusted startup configuration; the in-container preflight is mandatory.
func (c *coreComponent) configurePrerender(ctx context.Context, runtime *platform.Runtime) error {
	settings := c.config.Prerender
	secret := c.secret
	if secret == "" {
		secret = c.config.Core.BootstrapSecret
	}
	session, _, err := runtime.Identity().Authenticate(ctx, secret)
	if err != nil {
		return err
	}
	worker, err := prerender.NewDockerWorker(ctx, prerender.Config{
		Sandbox: prerender.DockerSandboxConfig{
			Image: settings.SandboxImage, Platform: settings.SandboxPlatform, DockerBinary: settings.DockerBinary,
			StagingRoot: settings.StagingDirectory, CPUs: settings.CPUs,
			MemoryLimitBytes: settings.MemoryLimitBytes, PIDsLimit: settings.PIDsLimit,
			MaxDocumentBytes: settings.MaxDocumentBytes, Timeout: settings.Timeout,
		},
		Limits: prerender.Limits{
			MaxPlanBytes: settings.MaxPlanBytes, MaxArtifacts: settings.MaxArtifacts,
			MaxArtifactBytes: settings.MaxArtifactBytes, MaxTotalBytes: settings.MaxTotalBytes,
		},
		Files: corePrerenderArtifactWriter{dispatcher: bus.NewModuleDispatcher(c.dispatcher, prerender.DataModuleID)},
	})
	if err != nil {
		return err
	}
	c.setCoreFiles(prerender.DataModuleID, coreSessionFileAdapter{files: runtime.Files(), session: session})
	c.prerenderHandle.Store(worker)
	return nil
}

type corePrerenderArtifactWriter struct {
	dispatcher contracts.Dispatcher
}

func (w corePrerenderArtifactWriter) WriteArtifact(ctx context.Context, requestKey, path, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	if w.dispatcher == nil {
		return "", errors.New("Core artifact writer is not configured")
	}
	// Core requires an opaque UUID request key. Derive it from the stable task
	// identity and artifact path so retries remain idempotent without letting
	// task payloads choose the key.
	key := uuid.NewSHA1(uuid.NameSpaceURL, []byte("website-prerender/"+requestKey)).String()
	streams := bus.NewScopedStreams()
	response, err := contracts.CallFileCreateVerified(ctx, w.dispatcher, streams, contracts.FileCreateVerifiedInput{
		RequestKey: key, Groups: []string{"features"},
		Metadata:    map[string]string{"artifact_path": path, "artifact_kind": "website_prerender"},
		ContentType: contentType, Size: size, SHA256: checksum,
	}, body)
	if err != nil {
		return "", err
	}
	return response.FileID, nil
}

func (c *coreComponent) setCoreFiles(moduleID string, files contracts.CoreFiles) {
	c.fileServiceMu.Lock()
	if c.fileServices == nil {
		c.fileServices = make(map[string]contracts.CoreFiles)
	}
	c.fileServices[moduleID] = files
	c.fileServiceMu.Unlock()
}

func (c *coreComponent) coreFiles(_ context.Context, moduleID string) (contracts.CoreFiles, error) {
	if c == nil || !isCoreFileConsumer(moduleID) {
		return nil, contracts.ErrDispatcherUnavailable
	}
	c.fileServiceMu.RLock()
	files := c.fileServices[moduleID]
	c.fileServiceMu.RUnlock()
	if files == nil {
		return nil, contracts.ErrDispatcherUnavailable
	}
	return files, nil
}

func (c *coreComponent) coreFileCreator(ctx context.Context, moduleID string) (contracts.VerifiedFileCreator, error) {
	return c.coreFiles(ctx, moduleID)
}

func (c *coreComponent) coreFileReader(ctx context.Context, moduleID string) (contracts.CoreFileReader, error) {
	return c.coreFiles(ctx, moduleID)
}

func (c *coreComponent) coreFileStat(ctx context.Context, moduleID string, subject contracts.AuthorizationSubject) (contracts.CoreFileStat, error) {
	if c == nil || c.runtime == nil || !isCoreFileConsumer(moduleID) {
		return nil, contracts.ErrDispatcherUnavailable
	}
	if subject.Credential == "" && subject.SessionToken == "" {
		return c.coreFiles(ctx, moduleID)
	}
	if subject.Validate() != nil {
		return nil, contracts.ErrAccessInvalid
	}
	var session platform.Session
	var err error
	if subject.Credential != "" {
		session, _, err = c.runtime.Identity().Authenticate(ctx, subject.Credential)
	} else {
		session, _, err = c.runtime.Sessions().AuthenticateSession(ctx, subject.SessionToken)
	}
	if err != nil {
		return nil, err
	}
	return coreSubjectFileStat{files: c.runtime.Files(), session: session}, nil
}

func (c *coreComponent) coreFileDelete(ctx context.Context, moduleID string) (contracts.CoreFileDeleter, error) {
	return c.coreFiles(ctx, moduleID)
}

// MediaSandboxState exposes the verified probe result to Website. It is not
// derived from a client request and never appears in a public DTO.
func (c *coreComponent) MediaSandboxState() (websiteapi.MediaSandboxState, error) {
	sandbox := c.mediaSandbox.Load()
	if sandbox == nil || !sandbox.Isolated() {
		return websiteapi.MediaSandboxState{}, errors.New("media sandbox is not verified")
	}
	state := sandbox.SandboxState()
	return websiteapi.MediaSandboxState{
		Backend: state.Backend, Image: state.Image, SeccompMode: state.SeccompMode,
		CapEff: state.CapEff, NoNewPrivs: state.NoNewPrivs, CPUQuota: state.CPUQuota,
		MemoryLimitBytes: state.MemoryLimitBytes, PIDsLimit: state.PIDsLimit,
		ReadOnlyRootFS: state.ReadOnlyRootFS, NetworkDenied: state.NetworkDenied,
		PrivateStaging0700: state.PrivateStaging0700, VerifiedAt: state.VerifiedAt,
	}, nil
}

func (c *coreComponent) Ready(ctx context.Context) error {
	if c == nil || c.runtime == nil || c.dispatcher == nil {
		return errors.New("Core is not initialized")
	}
	response, err := contracts.Call(ctx, c.dispatcher, contracts.CoreReadinessOperation(), contracts.CoreReadinessRequest{})
	if err != nil {
		return fmt.Errorf("Core Coordinator startup is not ready: %w", err)
	}
	if !response.Ready {
		return errors.New("Core readiness operation returned not ready")
	}
	return nil
}

func (c *coreComponent) ActivateAfterComponents(ctx context.Context) error {
	if c == nil || c.runtime == nil {
		return contracts.ErrWorkflowUnavailable
	}
	return c.runtime.ActivateCoordinator(ctx)
}

func (c *coreComponent) QuiesceBeforeComponents(ctx context.Context) error {
	if c == nil || c.runtime == nil {
		return nil
	}
	return c.runtime.StopCoordinator(ctx)
}

func (c *coreComponent) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if c.dispatcher != nil {
		if err := c.dispatcher.Close(ctx); err != nil {
			return err
		}
		c.dispatcher = nil
	}
	if c.runtime == nil {
		return nil
	}
	if err := c.runtime.CloseContext(ctx); err != nil {
		return err
	}
	c.runtime = nil
	return nil
}

func (c *coreComponent) IsReady() bool {
	return c != nil && c.runtime != nil && c.runtime.CoordinatorReady()
}

func (c *coreComponent) Errors() <-chan error {
	if c == nil || c.runtime == nil {
		return idleFailureStream()
	}
	return c.runtime.CoordinatorErrors()
}

// idleFailureStream satisfies the Coordinator's failure-source contract for
// components that have no asynchronous failures to report. Closing the stream
// would be interpreted as a fatal component failure.
func idleFailureStream() <-chan error { return make(chan error) }

type loaderComponent struct {
	core    *coreComponent
	config  appconfig.Resolved
	service *loaderapp.Service
	cancel  context.CancelFunc
	cleanup <-chan error
}

func (c *loaderComponent) Start(ctx context.Context) error {
	if c.core == nil || c.core.runtime == nil {
		return errors.New("Core is not initialized")
	}
	moduleData, err := c.core.registerModuleData(loaderapp.RecordDescriptor())
	if err != nil {
		return err
	}
	service, err := loaderapp.New(loaderIdentity{runtime: c.core.runtime}, loaderFiles{runtime: c.core.runtime}, moduleData, c.config.Loader)
	if err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithCancel(ctx)
	c.service, c.cancel, c.cleanup = service, cancel, service.RunCleanup(cleanupCtx)
	return nil
}

func (c *loaderComponent) Ready(context.Context) error {
	if c.service == nil {
		return errors.New("Loader is not initialized")
	}
	return nil
}

func (c *loaderComponent) Stop(context.Context) error {
	if c == nil || c.cancel == nil {
		return nil
	}
	c.cancel()
	for range c.cleanup {
	}
	c.service, c.cleanup = nil, nil
	c.cancel = nil
	return nil
}

func (c *loaderComponent) IsReady() bool { return c != nil && c.service != nil }

type fileAPIComponent struct {
	loader              *loaderComponent
	core                *coreComponent
	workflowControl     workflowControl
	formsEngine         *formsEngineComponent
	websiteEngine       *websiteEngineComponent
	contentEngine       *contentEngineComponent
	routerEngine        *routerEngineComponent
	publicationEngine   *publicationComponent
	retirementEngine    *retirementComponent
	formsAdminEnabled   bool
	websiteAdminEnabled bool
	identityAdmin       bool
	notifications       *transport.NotificationHub
	developmentFixtures bool
	routerAdminEnabled  bool
	uiHandler           http.Handler
	config              appconfig.Resolved
	observer            *observability.Observer
	server              *transport.AdminServer
}

func (c *fileAPIComponent) Start(ctx context.Context) error {
	if c.loader == nil || c.loader.service == nil {
		return errors.New("Loader is not initialized")
	}
	var fixtureAPIs *developmentFixtureAPIs
	if c.developmentFixtures {
		if !DevelopmentFixturesEnabled() || c.core == nil || c.core.runtime == nil {
			return errors.New("development-fixture APIs are not available in this build")
		}
		var err error
		fixtureAPIs, err = createDevelopmentFixtures(c.core.runtime, c.core.config)
		if err != nil {
			return fmt.Errorf("initialize read-only development fixtures: %w", err)
		}
	}
	adminConfig := transport.AdminConfig{
		Server: transport.ServerConfig{
			Address: c.config.HTTP.ListenAddress, ReadHeaderTimeout: c.config.HTTP.ReadHeaderTimeout,
			ReadTimeout: c.config.HTTP.ReadTimeout, WriteTimeout: c.config.HTTP.WriteTimeout,
			IdleTimeout: c.config.HTTP.IdleTimeout, MaxHeaderBytes: c.config.HTTP.MaxHeaderBytes,
		},
		FileAPI: c.config.FileAPI, AllowedOrigins: append([]string(nil), c.config.AdminAPI.AllowedOrigins...),
		MaxJSONBytes: c.config.AdminAPI.MaxJSONBytes, DefaultPageSize: c.config.AdminAPI.DefaultPageSize,
		MaxPageSize: c.config.AdminAPI.MaxPageSize, SessionCookieSecure: c.config.AdminAPI.SessionCookieSecure,
		FormsExportMaxConcurrent: c.config.FormsAPI.ExportMaxConcurrent, FormsExportMaxRows: c.config.FormsAPI.ExportMaxRows,
		FormsExportMaxBytes: c.config.FormsAPI.ExportMaxBytes, WorkflowAccessGroup: c.config.Coordinator.WorkflowAccessGroup,
		WorkflowPageDefault: c.config.Coordinator.WorkflowsPageDefault, WorkflowPageMaximum: c.config.Coordinator.WorkflowsPageMaximum,
		RouterAdminEnabled: c.routerAdminEnabled,
		UIHandler: c.uiHandler, SyntheticData: c.developmentFixtures, IdentityEnabled: c.identityAdmin,
		BuildIdentity: os.Getenv("ZUBRIUM_BUILD_IDENTITY"), Observer: c.observer,
	}
	var routerOperations *router.PublicOperationRegistry
	if c.routerEngine != nil {
		routerOperations = c.routerEngine.registry
	}
	adminConfig.Router = routerPresentationConfig(c.config, routerOperations)
	identity := adminIdentityAdapter{runtime: c.core.runtime}
	dependencies := transport.AdminDependencies{
		CoreReady: c.core.runtime.CoordinatorReady, Identity: identity, Sessions: identity,
		GroupAccess:              adminGroupAccessResolver{runtime: c.core.runtime, groups: configuredGroupNames(c.core.config.Core.AccessGroups)},
		CoordinatorAuthorization: adminCoordinatorAuthorization{runtime: c.core.runtime},
		Files:                    c.loader.service, WorkflowControl: c.workflowControl,
		Notifications: c.notifications, SnapshotForkWorkflow: snapshotForkWorkflowStarter{core: c.core},
	}
	if c.retirementEngine != nil {
		dependencies.Retirement = c.retirementEngine.client
	}
	if fixtureAPIs != nil {
		dependencies.Website, dependencies.Content, dependencies.Forms = fixtureAPIs.website, fixtureAPIs.content, fixtureAPIs.forms
		dependencies.ContentSchemas = fixtureAPIs.content
	} else {
		if c.websiteAdminEnabled && c.websiteEngine != nil {
			dependencies.Website = c.websiteEngine.service
		}
		if c.contentEngine != nil && c.contentEngine.engine != nil {
			dependencies.Content = c.contentEngine.engine
			dependencies.ContentSchemas = c.contentEngine.projections
		}
		if c.formsAdminEnabled && c.formsEngine != nil {
			dependencies.Forms = c.formsEngine.engine
		}
	}
	if c.routerAdminEnabled && c.routerEngine != nil {
		dependencies.Router, dependencies.RouterOperations = c.routerEngine.api, c.routerEngine.registry
	}
	server, err := transport.NewAdminServer(adminConfig, dependencies)
	if err != nil {
		return err
	}
	c.server = server
	return server.Start(ctx)
}

type workflowControl interface {
	List(context.Context, contracts.AdminWorkflowListFilter) (contracts.AdminWorkflowPage, error)
	Get(context.Context, uuid.UUID, contracts.AdminWorkflowDetailFilter) (contracts.AdminWorkflowDetail, error)
	RetryTask(context.Context, uuid.UUID, uuid.UUID) error
}

type busWorkflowControl struct{ dispatcher contracts.Dispatcher }

func (c busWorkflowControl) List(ctx context.Context, filter contracts.AdminWorkflowListFilter) (contracts.AdminWorkflowPage, error) {
	request := contracts.WorkflowListRequest{State: filter.State, Limit: filter.Limit}
	if filter.Before != nil {
		request.Before = &contracts.WorkflowCursor{ID: filter.Before.ID.String(), CreatedAt: filter.Before.CreatedAt}
	}
	response, err := contracts.Call(ctx, c.dispatcher, contracts.WorkflowListOperation(), request)
	if err != nil {
		return contracts.AdminWorkflowPage{}, err
	}
	var page contracts.AdminWorkflowPage
	if err := json.Unmarshal(response.Payload, &page); err != nil {
		return contracts.AdminWorkflowPage{}, err
	}
	return page, nil
}

func (c busWorkflowControl) Get(ctx context.Context, id uuid.UUID, filter contracts.AdminWorkflowDetailFilter) (contracts.AdminWorkflowDetail, error) {
	request := contracts.WorkflowGetRequest{WorkflowID: id.String(), Limit: filter.Limit}
	if filter.Before != nil {
		request.Before = &contracts.TaskCursor{ID: filter.Before.ID.String(), CreatedAt: filter.Before.CreatedAt}
	}
	response, err := contracts.Call(ctx, c.dispatcher, contracts.WorkflowGetOperation(), request)
	if err != nil {
		return contracts.AdminWorkflowDetail{}, err
	}
	var detail contracts.AdminWorkflowDetail
	if err := json.Unmarshal(response.Payload, &detail); err != nil {
		return contracts.AdminWorkflowDetail{}, err
	}
	return detail, nil
}

func (c busWorkflowControl) RetryTask(ctx context.Context, workflowID, taskID uuid.UUID) error {
	_, err := contracts.Call(ctx, c.dispatcher, contracts.WorkflowRetryOperation(), contracts.WorkflowRetryRequest{
		WorkflowID: workflowID.String(), TaskID: taskID.String(),
	})
	return err
}

func (c *fileAPIComponent) Ready(context.Context) error {
	if c == nil || c.server == nil || !c.server.IsReady() {
		return errors.New("Admin transport is not initialized")
	}
	return nil
}

func (c *fileAPIComponent) Stop(ctx context.Context) error {
	if c == nil || c.server == nil {
		return nil
	}
	return c.server.Stop(ctx)
}

func (c *fileAPIComponent) IsReady() bool {
	return c != nil && c.server != nil && c.server.IsReady()
}

func (c *fileAPIComponent) setApplicationReadiness(readiness transport.Readiness) {
	if c != nil && c.server != nil {
		c.server.SetApplicationReadiness(readiness)
	}
}

func (c *fileAPIComponent) Errors() <-chan error {
	if c == nil || c.server == nil {
		return idleFailureStream()
	}
	return c.server.Errors()
}

func registerComponents(resolved appconfig.Resolved, plan platform.BuildPlan, observer *observability.Observer) (*platform.RuntimeRegistry, *coreComponent, *loaderComponent, *fileAPIComponent, error) {
	if len(plan.Components) == 0 {
		return nil, nil, nil, nil, platform.ErrCoordinatorInputInvalid
	}
	registry := platform.NewRuntimeRegistry()
	rawRegister := registry.Register
	register := func(component platform.RuntimeComponent) error {
		original := component.New
		component.New = func(ctx context.Context) (platform.ManagedComponent, error) {
			if observer == nil {
				return original(ctx)
			}
			identity := observability.Identity{
				ModuleKind: componentModuleKind(component.ID), ModuleID: componentModuleID(component.ID),
				ComponentID: component.ID, OperationID: "component.initialize",
			}
			var managed platform.ManagedComponent
			err := observer.Operation(ctx, identity, "component.lifecycle", nil, func(operationCtx context.Context) error {
				var createErr error
				managed, createErr = original(operationCtx)
				return createErr
			})
			return managed, err
		}
		return rawRegister(component)
	}
	selected := make(map[string]struct{}, len(plan.Components))
	for _, id := range plan.Components {
		selected[id] = struct{}{}
	}
	core := &coreComponent{config: resolved, selected: selected, dispatcher: nil}
	operations, err := registerWorkerHandlers(selected, resolved, core, observer)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("register worker handlers: %w", err)
	}
	core.operations = operations
	keys := []contracts.Key{contracts.CoreReadinessOperation().Key}
	for key := range operations {
		keys = append(keys, key)
	}
	if _, ok := selected["admin.coordinator"]; ok {
		keys = append(keys, contracts.WorkflowListOperation().Key, contracts.WorkflowGetOperation().Key, contracts.WorkflowRetryOperation().Key)
	}
	if _, ok := selected["public.content-runtime"]; ok {
		keys = append(keys, contracts.PublishedContentReadOperation().Key)
	}
	if _, enabled := selected["engine.publication"]; enabled {
		keys = append(keys, contracts.PublicationPublishOperation().Key)
	}
	if _, enabled := selected["composition.retirement"]; enabled {
		keys = append(keys, contracts.RetirePageOperation().Key, contracts.GetPageRetirementOperation().Key)
	}
	if hasModuleDataConsumers(selected) {
		keys = append(keys,
			contracts.RecordGetOperation().Key, contracts.RecordGetExpiredOperation().Key,
			contracts.RecordPutOperation().Key, contracts.RecordDeleteOperation().Key,
			contracts.RecordBatchOperation().Key, contracts.RecordListOperation().Key,
			contracts.RecordListExpiredOperation().Key,
		)
	}
	if hasCoreFileConsumers(selected) {
		keys = append(keys,
			contracts.FileCreateVerifiedOperation().Key, contracts.FileReadOperation().Key, contracts.FileStatOperation().Key, contracts.FileDeleteOperation().Key,
			contracts.FileSetGetOperation().Key, contracts.FileSetListOperation().Key, contracts.FileSetMembersOperation().Key,
			contracts.FileSetAddOperation().Key, contracts.FileSetRemoveOperation().Key,
		)
	}
	if hasCoreAccessConsumers(selected) {
		keys = append(keys,
			contracts.AccessAuthenticateOperation().Key,
			contracts.AccessAuthenticateSessionOperation().Key,
			contracts.AccessRequireGroupOperation().Key,
		)
	}
	dispatcher, err := bus.NewAllowlisted(keys...)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := dispatcher.EnableScopedStreams(); err != nil {
		return nil, nil, nil, nil, err
	}
	if len(operations) > 0 {
		handlers := make(map[contracts.Key]bus.TaskOperationHandler, len(operations))
		for key, binding := range operations {
			handlers[key] = binding.Handler
		}
		if err := bus.RegisterTaskOperations(dispatcher, handlers); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("register DataBus task operations: %w", err)
		}
	}
	dispatcher.WithObserver(observer)
	if err := validateRunnableComposition(selected); err != nil {
		return nil, nil, nil, nil, err
	}
	core.dispatcher = dispatcher
	loader := &loaderComponent{core: core, config: resolved}
	fileAPI := &fileAPIComponent{loader: loader, core: core, config: resolved, observer: observer}
	_, fileAPI.developmentFixtures = selected["development.fixtures"]
	if _, enabled := selected["admin.notifications"]; enabled {
		fileAPI.notifications = transport.NewNotificationHub()
	}
	_, fileAPI.formsAdminEnabled = selected["admin.forms"]
	_, fileAPI.websiteAdminEnabled = selected["admin.website"]
	_, fileAPI.identityAdmin = selected["admin.identity"]
	if fileAPI.developmentFixtures && !DevelopmentFixturesEnabled() {
		return nil, nil, nil, nil, errors.New("development fixtures were selected without the dedicated development build")
	}
	_, fileAPI.routerAdminEnabled = selected["admin.router"]
	if _, enabled := selected["admin.coordinator"]; enabled {
		fileAPI.workflowControl = busWorkflowControl{dispatcher: dispatcher}
	}
	if err := register(platform.RuntimeComponent{ID: "core", New: func(context.Context) (platform.ManagedComponent, error) { return core, nil }}); err != nil {
		return nil, nil, nil, nil, err
	}
	for _, workerID := range []string{"worker.archive", "worker.media", "worker.prerender"} {
		if _, ok := selected[workerID]; !ok {
			continue
		}
		workerID := workerID
		if err := register(platform.RuntimeComponent{ID: workerID, Requires: []string{"core"}, New: func(ctx context.Context) (platform.ManagedComponent, error) {
			return newWorkerComponent(ctx, workerID)
		}}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if err := register(platform.RuntimeComponent{ID: "engine.loader", Requires: []string{"core"}, New: func(context.Context) (platform.ManagedComponent, error) { return loader, nil }}); err != nil {
		return nil, nil, nil, nil, err
	}
	if fileAPI.developmentFixtures {
		for _, id := range []string{"development.fixtures", "engine.website", "engine.content", "engine.forms"} {
			id := id
			requires := []string{"core"}
			component := &readyComponent{id: id}
			if err := register(platform.RuntimeComponent{ID: id, Requires: requires, New: func(context.Context) (platform.ManagedComponent, error) { return component, nil }}); err != nil {
				return nil, nil, nil, nil, err
			}
		}
	}
	var websiteEngine *websiteEngineComponent
	if _, enabled := selected["engine.website"]; enabled && !fileAPI.developmentFixtures {
		websiteEngine = newWebsiteEngineComponent(core, resolved)
		fileAPI.websiteEngine = websiteEngine
		if err := register(platform.RuntimeComponent{ID: "engine.website", Requires: []string{"core"}, New: func(context.Context) (platform.ManagedComponent, error) { return websiteEngine, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	var contentEngine *contentEngineComponent
	if _, enabled := selected["engine.content"]; enabled && !fileAPI.developmentFixtures {
		if websiteEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		contentEngine = newContentEngineComponent(core, resolved, nil)
		fileAPI.contentEngine = contentEngine
		if _, enabled := selected["public.content-runtime"]; enabled {
			if err := bus.RegisterPublishedContent(dispatcher, func(ctx context.Context, pageSnapshotID, locale string) (json.RawMessage, error) {
				content, err := contentEngine.ReadPublishedContent(ctx, pageSnapshotID, locale)
				if err != nil {
					return nil, err
				}
				return json.Marshal(content)
			}); err != nil {
				return nil, nil, nil, nil, err
			}
		}
		if err := register(platform.RuntimeComponent{ID: "engine.content", Requires: []string{"core", "engine.website"}, New: func(context.Context) (platform.ManagedComponent, error) {
			contentEngine.website = websiteEngine.service
			return contentEngine, nil
		}}); err != nil {
			return nil, nil, nil, nil, err
		}
		contentEngine.buildLocaleCatalog = websiteEngine.localeCatalog
		contentEngine.buildProjection = websiteEngine.contentBuildView
	}
	if _, enabled := selected["admin.website"]; enabled && !fileAPI.developmentFixtures {
		if websiteEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		adminWebsite := &readyComponent{id: "admin.website"}
		if err := register(platform.RuntimeComponent{ID: "admin.website", Requires: []string{"core", "engine.website", "admin.files"}, New: func(context.Context) (platform.ManagedComponent, error) { return adminWebsite, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	var formsEngine *formsEngineComponent
	if _, enabled := selected["engine.forms"]; enabled && !fileAPI.developmentFixtures {
		formsEngine = &formsEngineComponent{core: core, config: resolved}
		fileAPI.formsEngine = formsEngine
		if err := register(platform.RuntimeComponent{ID: "engine.forms", Requires: []string{"core"}, New: func(context.Context) (platform.ManagedComponent, error) { return formsEngine, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	adminFilesRequires := []string{"core", "engine.loader"}
	if formsEngine != nil && fileAPI.formsAdminEnabled {
		adminFilesRequires = append(adminFilesRequires, "engine.forms")
	}
	if err := register(platform.RuntimeComponent{ID: "admin.files", Requires: adminFilesRequires, New: func(context.Context) (platform.ManagedComponent, error) { return fileAPI, nil }}); err != nil {
		return nil, nil, nil, nil, err
	}
	if _, enabled := selected["admin.ui"]; enabled {
		handler, err := newAdminUIHandler()
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("initialize Admin UI: %w", err)
		}
		if handler == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		fileAPI.uiHandler = handler
		if err := register(platform.RuntimeComponent{ID: "admin.ui", Requires: []string{"core", "admin.files"}, New: func(context.Context) (platform.ManagedComponent, error) {
			return &readyComponent{id: "admin.ui"}, nil
		}}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["admin.coordinator"]; enabled {
		component := &readyComponent{id: "admin.coordinator"}
		if err := register(platform.RuntimeComponent{ID: "admin.coordinator", Requires: []string{"core", "admin.files"}, New: func(context.Context) (platform.ManagedComponent, error) { return component, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if fileAPI.identityAdmin {
		component := &readyComponent{id: "admin.identity"}
		if err := register(platform.RuntimeComponent{ID: "admin.identity", Requires: []string{"core", "admin.files"}, New: func(context.Context) (platform.ManagedComponent, error) { return component, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if fileAPI.notifications != nil {
		notificationsAPI := &readyComponent{id: "admin.notifications"}
		if err := register(platform.RuntimeComponent{ID: "admin.notifications", Requires: []string{"core", "admin.files", "admin.identity"}, New: func(context.Context) (platform.ManagedComponent, error) { return notificationsAPI, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if formsEngine != nil && fileAPI.formsAdminEnabled && !fileAPI.developmentFixtures {
		adminForms := &readyComponent{id: "admin.forms"}
		if err := register(platform.RuntimeComponent{ID: "admin.forms", Requires: []string{"core", "engine.forms", "admin.files"}, New: func(context.Context) (platform.ManagedComponent, error) { return adminForms, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["admin.content"]; enabled && !fileAPI.developmentFixtures {
		if contentEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		adminContent := &readyComponent{id: "admin.content"}
		if err := register(platform.RuntimeComponent{ID: "admin.content", Requires: []string{"core", "engine.website", "engine.content", "admin.files"}, New: func(context.Context) (platform.ManagedComponent, error) { return adminContent, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if fileAPI.developmentFixtures {
		for _, item := range []struct {
			id       string
			engineID string
		}{
			{id: "admin.website", engineID: "engine.website"},
			{id: "admin.content", engineID: "engine.content"},
			{id: "admin.forms", engineID: "engine.forms"},
		} {
			component := &readyComponent{id: item.id}
			if err := register(platform.RuntimeComponent{ID: item.id, Requires: []string{"core", "admin.files", item.engineID}, New: func(context.Context) (platform.ManagedComponent, error) { return component, nil }}); err != nil {
				return nil, nil, nil, nil, err
			}
		}
	}
	var routerEngine *routerEngineComponent
	if _, enabled := selected["engine.router"]; enabled {
		requires := []string{"core", "engine.loader"}
		if formsEngine != nil {
			requires = append(requires, "engine.forms")
		}
		if contentEngine != nil {
			requires = append(requires, "engine.content")
		}
		routerEngine = &routerEngineComponent{core: core, loader: loader, forms: formsEngine, content: contentEngine, config: resolved, selected: selected, dispatcher: dispatcher}
		fileAPI.routerEngine = routerEngine
		if err := register(platform.RuntimeComponent{ID: "engine.router", Requires: requires, New: func(context.Context) (platform.ManagedComponent, error) { return routerEngine, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["engine.publication"]; enabled {
		if websiteEngine == nil || routerEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		publicationEngine := &publicationComponent{
			core: core, config: resolved, website: websiteEngine, router: routerEngine, dispatcher: dispatcher,
		}
		if err := bus.Register(dispatcher, contracts.PublicationPublishOperation(), publicationEngine.publish); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("register publication operation: %w", err)
		}
		fileAPI.publicationEngine = publicationEngine
		if err := register(platform.RuntimeComponent{ID: "engine.publication", Requires: []string{"core", "engine.website", "engine.content", "engine.router"}, New: func(context.Context) (platform.ManagedComponent, error) {
			return publicationEngine, nil
		}}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["composition.retirement"]; enabled {
		if websiteEngine == nil || routerEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		retirementEngine := &retirementComponent{
			core: core, config: resolved, website: websiteEngine, router: routerEngine, dispatcher: dispatcher,
		}
		if err := bus.Register(dispatcher, contracts.RetirePageOperation(), retirementEngine.retirePage); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("register page retirement operation: %w", err)
		}
		if err := bus.Register(dispatcher, contracts.GetPageRetirementOperation(), retirementEngine.getPageRetirement); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("register page retirement lookup operation: %w", err)
		}
		fileAPI.retirementEngine = retirementEngine
		if err := register(platform.RuntimeComponent{ID: "composition.retirement", Requires: []string{"core", "engine.website", "engine.router"}, New: func(context.Context) (platform.ManagedComponent, error) {
			return retirementEngine, nil
		}}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["admin.router"]; enabled {
		if routerEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		adminAPI := &routerAdminComponent{}
		if err := register(platform.RuntimeComponent{ID: "admin.router", Requires: []string{"core", "engine.loader", "admin.files", "engine.router"}, New: func(context.Context) (platform.ManagedComponent, error) { return adminAPI, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["public.router"]; enabled {
		if routerEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		publicAPI := &publicRouterComponent{core: core, engine: routerEngine, config: resolved, observer: observer}
		if err := register(platform.RuntimeComponent{ID: "public.router", Requires: []string{"engine.router"}, New: func(context.Context) (platform.ManagedComponent, error) { return publicAPI, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["public.content-runtime"]; enabled {
		if routerEngine == nil || contentEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		if err := register(platform.RuntimeComponent{ID: "public.content-runtime", Requires: []string{"core", "engine.website", "engine.content", "engine.router", "public.router"}, New: func(context.Context) (platform.ManagedComponent, error) {
			return &readyComponent{id: "public.content-runtime"}, nil
		}}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["public.form-upload"]; enabled {
		if routerEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		uploadAPI := &publicUploadComponent{engine: routerEngine}
		requires := []string{"engine.router", "public.router"}
		if formsEngine != nil {
			requires = append(requires, "engine.forms")
		}
		if err := register(platform.RuntimeComponent{ID: "public.form-upload", Requires: requires, New: func(context.Context) (platform.ManagedComponent, error) { return uploadAPI, nil }}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["public.form-session"]; enabled {
		if routerEngine == nil || formsEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		if err := register(platform.RuntimeComponent{ID: "public.form-session", Requires: []string{"engine.router", "public.router", "engine.forms"}, New: func(context.Context) (platform.ManagedComponent, error) {
			return &readyComponent{id: "public.form-session"}, nil
		}}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if _, enabled := selected["public.form-submit"]; enabled {
		if routerEngine == nil || formsEngine == nil {
			return nil, nil, nil, nil, platform.ErrComponentUnavailable
		}
		if err := register(platform.RuntimeComponent{ID: "public.form-submit", Requires: []string{"engine.router", "public.router", "engine.forms"}, New: func(context.Context) (platform.ManagedComponent, error) {
			return &readyComponent{id: "public.form-submit"}, nil
		}}); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	return registry, core, loader, fileAPI, nil
}

func validateRunnableComposition(selected map[string]struct{}) error {
	for _, component := range []string{"core", "engine.loader", "admin.files"} {
		if _, ok := selected[component]; !ok {
			return fmt.Errorf("selected build profile requires %s for the current command", component)
		}
	}
	available := map[string]struct{}{
		"core": {}, "engine.loader": {}, "admin.files": {}, "admin.coordinator": {}, "admin.identity": {}, "admin.notifications": {},
		"engine.router": {}, "engine.forms": {}, "engine.website": {}, "engine.content": {}, "engine.publication": {}, "composition.retirement": {}, "admin.website": {}, "admin.content": {}, "admin.forms": {}, "admin.router": {}, "admin.ui": {}, "public.router": {}, "public.form-upload": {}, "public.content-runtime": {},
		"public.form-session": {}, "public.form-submit": {},
		"worker.archive": {}, "worker.media": {}, "worker.prerender": {},
	}
	if _, fixtures := selected["development.fixtures"]; fixtures {
		for _, component := range []string{"development.fixtures", "engine.website", "engine.content", "engine.forms", "admin.website", "admin.content", "admin.forms"} {
			available[component] = struct{}{}
		}
	}
	for component := range selected {
		if _, ok := available[component]; !ok {
			return fmt.Errorf("selected component %q has no runtime implementation in this composition", component)
		}
	}
	return nil
}

func taskClasses(operations map[contracts.Key]TaskOperationBinding) []string {
	seen := make(map[string]struct{}, len(operations))
	classes := make([]string, 0, len(operations))
	for _, binding := range operations {
		if _, exists := seen[binding.TaskClass]; exists {
			continue
		}
		seen[binding.TaskClass] = struct{}{}
		classes = append(classes, binding.TaskClass)
	}
	sort.Strings(classes)
	return classes
}

type readyComponent struct {
	id    string
	ready bool
}

func (component *readyComponent) Start(context.Context) error {
	if component == nil || component.id == "" {
		return platform.ErrComponentUnavailable
	}
	component.ready = true
	return nil
}
func (component *readyComponent) Ready(context.Context) error {
	if component == nil || !component.ready {
		return platform.ErrComponentUnavailable
	}
	return nil
}
func (component *readyComponent) Stop(context.Context) error {
	if component != nil {
		component.ready = false
	}
	return nil
}

func componentModuleKind(componentID string) observability.ModuleKind {
	switch {
	case componentID == "core" || strings.HasPrefix(componentID, "core."):
		return observability.ModuleKindCore
	case strings.HasPrefix(componentID, "engine."):
		return observability.ModuleKindEngine
	case strings.HasPrefix(componentID, "worker."):
		return observability.ModuleKindWorker
	case strings.HasPrefix(componentID, "admin.") || strings.HasPrefix(componentID, "public."):
		return observability.ModuleKindPresentation
	default:
		return observability.ModuleKindComposition
	}
}

func componentModuleID(componentID string) string {
	if componentID == "core" {
		return "core"
	}
	if strings.HasPrefix(componentID, "engine.") || strings.HasPrefix(componentID, "worker.") {
		return componentID
	}
	if strings.HasPrefix(componentID, "admin.") {
		return "admin"
	}
	if strings.HasPrefix(componentID, "public.") {
		return "public-router"
	}
	return "composition"
}

func configuredGroupNames(groups map[string]platform.AccessLevel) []string {
	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	sort.Strings(names)
	return names
}
