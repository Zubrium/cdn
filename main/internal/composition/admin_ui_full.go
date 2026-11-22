//go:build full && !devfixtures && !router

package composition

import (
	"fmt"
	"net/http"

	"github.com/Zubrium/cdn/pkg/static"
	adminui "github.com/Zubrium/cdn/presentation/adminui"
)

func newAdminUIHandler() (http.Handler, error) {
	assets, err := static.Assets()
	if err != nil {
		return nil, fmt.Errorf("open embedded Admin UI assets: %w", err)
	}
	handler, err := adminui.NewHandler(assets)
	if err != nil {
		return nil, fmt.Errorf("initialize Admin UI handler: %w", err)
	}
	return handler, nil
}
