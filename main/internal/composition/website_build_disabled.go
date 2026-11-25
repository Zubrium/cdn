//go:build !full || devfixtures || router

package composition

import websiteapi "github.com/Zubrium/cdn/engines/website/presentation"

// Keep the Content projection ABI constant linked in profiles that omit the
// durable build workflow; Admin Website handlers still read the site model.
const _ = websiteapi.ContentProjectionVersion

const websiteBuildEnabled = false
