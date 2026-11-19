package composition

import (
	"context"
	"errors"

	platform "github.com/Zubrium/cdn/core/presentation"
	formsengine "github.com/Zubrium/cdn/engines/forms"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

type formsEngineComponent struct {
	core    *coreComponent
	config  appconfig.Resolved
	engine  *formsengine.Engine
	cancel  context.CancelFunc
	cleanup <-chan error
}

func (component *formsEngineComponent) Start(ctx context.Context) error {
	if component == nil || ctx == nil || component.core == nil || component.core.runtime == nil {
		return errors.New("Forms dependencies are not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	settings := component.config.FormsAPI
	if settings.FileServiceCredential == "" || settings.VisitorSessionTTL <= 0 || settings.VisitorSessionCleanupEvery <= 0 {
		return errors.New("Forms session and file service settings are required")
	}
	identity := component.core.runtime.EngineIdentity()
	fileSession, principal, err := identity.Authenticate(ctx, settings.FileServiceCredential)
	if err != nil {
		return errors.New("Forms file service credential could not be authenticated")
	}
	if principal.ID == "" || !principal.Active {
		return errors.New("Forms file service identity is inactive")
	}
	data, err := component.core.runtime.RegisterDataModule(formsengine.ModuleDataDescriptor())
	if err != nil {
		return err
	}
	engine, err := formsengine.New(formsengine.Dependencies{
		Identity: identity, Sessions: component.core.runtime.Sessions(),
		Authorization: component.core.runtime.GroupAuthorization(), Files: component.core.runtime.EngineFiles(),
		Data: data, FileSession: fileSession, VisitorSessionTTL: settings.VisitorSessionTTL,
	})
	if err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithCancel(ctx)
	cleanup := engine.RunVisitorSessionCleanup(cleanupCtx, settings.VisitorSessionCleanupEvery)
	component.engine, component.cancel, component.cleanup = engine, cancel, cleanup
	return nil
}

func (component *formsEngineComponent) Ready(context.Context) error {
	if component == nil || component.engine == nil {
		return errors.New("Forms Engine is not initialized")
	}
	return nil
}

func (component *formsEngineComponent) Stop(ctx context.Context) error {
	if component == nil || component.cancel == nil {
		return nil
	}
	component.cancel()
	for range component.cleanup {
	}
	component.engine, component.cleanup, component.cancel = nil, nil, nil
	return nil
}

func (component *formsEngineComponent) IsReady() bool {
	return component != nil && component.engine != nil
}

func (component *formsEngineComponent) Errors() <-chan error {
	if component == nil || component.cleanup == nil {
		closed := make(chan error)
		close(closed)
		return closed
	}
	return component.cleanup
}

var _ platform.ManagedComponent = (*formsEngineComponent)(nil)
