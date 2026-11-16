//go:build !full

package main

import "github.com/Zubrium/cdn/core/platform"

func registerProfile(_ *platform.Registry) error { return nil }
