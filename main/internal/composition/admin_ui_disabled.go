//go:build !full || devfixtures || router

package composition

import "net/http"

// Admin UI assets are compiled only into the full profile; other profiles keep
// the component unavailable instead of serving a placeholder handler.
func newAdminUIHandler() (http.Handler, error) { return nil, nil }
