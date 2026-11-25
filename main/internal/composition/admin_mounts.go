package composition

import (
	"context"
	"errors"
	"net/http"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
	coordinatoradmin "github.com/Zubrium/cdn/presentation/admin/base/admin/coordinator"
	adminapi "github.com/Zubrium/cdn/presentation/admin/base/admin/main"
	adminnotification "github.com/Zubrium/cdn/presentation/admin/base/notification"
	"github.com/Zubrium/cdn/presentation/admin/base/transport"
	contentadmin "github.com/Zubrium/cdn/presentation/admin/content"
	formsadmin "github.com/Zubrium/cdn/presentation/admin/forms"
	adminrouter "github.com/Zubrium/cdn/presentation/admin/router"
	fileapi "github.com/Zubrium/cdn/presentation/fileapi"
	routerhttp "github.com/Zubrium/cdn/presentation/router"
	sitesapi "github.com/Zubrium/cdn/presentation/sitesapi"
)

type adminMountSources struct {
	config                   transport.AdminConfig
	files                    fileapi.Loader
	identity                 adminapi.IdentityAdminAPI
	sessions                 adminapi.SessionAdminAPI
	groupAccess              adminapi.SessionGroupAccessResolver
	coordinatorAuthorization coordinatoradmin.GroupAuthorization
	workflowControl          workflowControl
	notifications            *adminnotification.Hub
	website                  contracts.AdminWebsiteAPI
	retirement               sitesapi.PageRetirementAPI
	snapshotFork             sitesapi.SnapshotForkWorkflowStarter
	content                  contracts.AdminContentAPI
	contentSchemas           contentadmin.SchemaSnapshots
	forms                    contracts.AdminFormsAPI
	router                   contracts.Contract
	routerOperations         *routerhttp.PublicOperationRegistry
	observer                 *observability.Observer
	syntheticData            bool
	identityEnabled          bool
	routerAdminEnabled       bool
}

func (sources adminMountSources) handlerMounts(parent transport.HandlerContext) ([]transport.HandlerMount, error) {
	var mounts []transport.HandlerMount
	add := func(patterns []string, handler http.Handler, guarded, synthetic bool) error {
		if handler == nil {
			return errors.New("Admin handler mount is nil")
		}
		mounts = append(mounts, transport.HandlerMount{
			Patterns: append([]string(nil), patterns...), Handler: handler,
			Guarded: guarded, Synthetic: synthetic,
		})
		return nil
	}
	fileConfig := sources.config.FileAPI
	fileConfig.AllowedOrigins = append([]string(nil), sources.config.AllowedOrigins...)
	if sources.files == nil {
		return nil, errors.New("Admin file API is not configured")
	}
	fileHandler, err := fileapi.NewHandler(sources.files, fileConfig)
	if err != nil {
		return nil, err
	}
	if err := add([]string{"/api/files", "/api/files/"}, fileHandler, true, false); err != nil {
		return nil, err
	}
	if sources.workflowControl != nil {
		authorize, err := coordinatoradmin.NewGroupAuthorizer(sources.coordinatorAuthorization, sources.config.WorkflowAccessGroup)
		if err != nil {
			return nil, err
		}
		sessionAuthorize := func(ctx context.Context, credential string, permission coordinatoradmin.Permission) error {
			if parent.AuthorizeWorkflowSession == nil {
				return contracts.ErrAccessForbidden
			}
			return parent.AuthorizeWorkflowSession(ctx, credential, string(permission))
		}
		handler, err := coordinatoradmin.NewHandler(sources.workflowControl, authorize, coordinatoradmin.Config{
			DefaultPageSize: sources.config.WorkflowPageDefault, MaxPageSize: sources.config.WorkflowPageMaximum,
			AllowedOrigins: append([]string(nil), sources.config.AllowedOrigins...), SessionAuthorize: sessionAuthorize,
		})
		if err != nil {
			return nil, err
		}
		if err := add([]string{"/api/coordinator/workflows", "/api/coordinator/workflows/"}, handler, true, false); err != nil {
			return nil, err
		}
	}
	if sources.identityEnabled {
		handler, err := adminapi.NewHandlerWithSessions(sources.identity, sources.sessions, adminapi.Config{
			MaxJSONBytes: sources.config.MaxJSONBytes, SessionCookieSecure: sources.config.SessionCookieSecure,
			AllowedOrigins: append([]string(nil), sources.config.AllowedOrigins...), GroupAccess: sources.groupAccess,
		})
		if err != nil {
			return nil, err
		}
		if err := add([]string{
			"/api/admin/session", "/api/admin/principals", "/api/admin/principals/",
			"/api/admin/credentials/", "/api/admin/access-groups",
		}, handler, true, sources.syntheticData); err != nil {
			return nil, err
		}
	}
	if sources.notifications != nil {
		handler, err := adminnotification.NewHandler(sources.notifications, sources.sessions, sources.identity, sources.config.AllowedOrigins)
		if err != nil {
			return nil, err
		}
		if err := add([]string{"/api/admin/notifications"}, handler, true, false); err != nil {
			return nil, err
		}
	}
	if sources.website != nil {
		handler, err := sitesapi.NewHandler(sources.website, sitesapi.Config{
			MaxJSONBytes: sources.config.MaxJSONBytes, DefaultPageSize: sources.config.DefaultPageSize,
			MaxPageSize: sources.config.MaxPageSize, AllowedOrigins: append([]string(nil), sources.config.AllowedOrigins...),
			Retirement: sources.retirement, SnapshotForkWorkflow: sources.snapshotFork,
		})
		if err != nil {
			return nil, err
		}
		if err := add([]string{
			"/api/admin/model", "/api/admin/model/", "/api/admin/pages", "/api/admin/pages/",
			"/api/admin/templates", "/api/admin/templates/", "/api/admin/snapshots", "/api/admin/snapshots/",
			"/api/admin/builds", "/api/admin/builds/", "/api/admin/features", "/api/admin/features/",
			"/api/admin/feature-imports", "/api/admin/feature-imports/", "/api/admin/media", "/api/admin/media/",
		}, handler, true, sources.syntheticData); err != nil {
			return nil, err
		}
	}
	if sources.content != nil {
		handler, err := contentadmin.NewHandler(sources.content, sources.contentSchemas, contentadmin.Config{
			MaxJSONBytes: sources.config.MaxJSONBytes, DefaultPageSize: sources.config.DefaultPageSize,
			MaxPageSize: sources.config.MaxPageSize, AllowedOrigins: append([]string(nil), sources.config.AllowedOrigins...),
		})
		if err != nil {
			return nil, err
		}
		if err := add([]string{"/api/admin/content/"}, handler, true, sources.syntheticData); err != nil {
			return nil, err
		}
	}
	if sources.forms != nil {
		handler, err := formsadmin.NewHandler(sources.forms, formsadmin.Config{
			MaxJSONBytes: sources.config.MaxJSONBytes, DefaultPageSize: sources.config.DefaultPageSize,
			MaxPageSize: sources.config.MaxPageSize, ExportMaxConcurrent: sources.config.FormsExportMaxConcurrent,
			ExportMaxRows: sources.config.FormsExportMaxRows, ExportMaxBytes: sources.config.FormsExportMaxBytes,
			AllowedOrigins: append([]string(nil), sources.config.AllowedOrigins...),
		})
		if err != nil {
			return nil, err
		}
		if err := add([]string{"/api/admin/forms/"}, handler, true, sources.syntheticData); err != nil {
			return nil, err
		}
	}
	if sources.routerAdminEnabled {
		if sources.router == nil {
			return nil, errors.New("Router Admin dependencies are not initialized")
		}
		routerConfig := sources.config.Router
		routerConfig.AllowedOrigins = append([]string(nil), sources.config.AllowedOrigins...)
		routerConfig.PublicOperations = sources.routerOperations
		handler, err := adminrouter.NewHandler(sources.router, routerConfig)
		if err != nil {
			return nil, err
		}
		if err := add([]string{"/api/admin/router", "/api/admin/router/"}, handler, true, false); err != nil {
			return nil, err
		}
	}
	return mounts, nil
}
