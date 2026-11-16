//go:build full

package main

import (
	"github.com/Zubrium/cdn/core/platform"
	content "github.com/Zubrium/cdn/engines/content/presentation"
	forms "github.com/Zubrium/cdn/engines/forms/presentation"
	website "github.com/Zubrium/cdn/engines/website/presentation"
)

func registerProfile(registry *platform.Registry) error {
	if err := registry.Register("website", website.Descriptor{}); err != nil {
		return err
	}
	if err := registry.Register("content", content.Descriptor{}); err != nil {
		return err
	}
	return registry.Register("forms", forms.Descriptor{})
}
