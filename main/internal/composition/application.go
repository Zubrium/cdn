package composition

import (
	platform "github.com/Zubrium/cdn/core/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

type StartupComponents struct {
	core *coreComponent
	api  *fileAPIComponent
}

func RegisterComponents(config appconfig.Resolved, plan platform.BuildPlan) (*platform.RuntimeRegistry, *StartupComponents, error) {
	registry, core, _, api, err := registerComponents(config, plan)
	if err != nil {
		return nil, nil, err
	}
	return registry, &StartupComponents{core: core, api: api}, nil
}

func (components *StartupComponents) SetApplicationReadiness(readiness interface{ Ready() bool }) {
	if components != nil && components.api != nil {
		components.api.setApplicationReadiness(readiness)
	}
}

func (components *StartupComponents) InitialAdminSecret() string {
	if components == nil || components.core == nil {
		return ""
	}
	return components.core.secret
}

func (components *StartupComponents) AdminAddress() string {
	if components == nil || components.api == nil || components.api.server == nil {
		return ""
	}
	return components.api.server.Address()
}
