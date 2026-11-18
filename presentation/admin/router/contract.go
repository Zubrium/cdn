// Package router exposes the authenticated Router Settings API independently
// from the anonymous public delivery listener.
package router

import (
	"net/http"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
	routerhttp "github.com/Zubrium/cdn/presentation/router"
)

type Engine = engine.Contract
type Config = routerhttp.Config

func NewHandler(api Engine, config Config) (http.Handler, error) {
	handler, err := routerhttp.NewHandler(api, config)
	if err != nil {
		return nil, err
	}
	return handler.AdminHTTPHandler(), nil
}
