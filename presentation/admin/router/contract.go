// Package router exposes the authenticated Router Settings API independently
// from the anonymous public delivery listener.
package router

import (
	"net/http"

	"github.com/Zubrium/cdn/pkg/contracts"
	routerhttp "github.com/Zubrium/cdn/presentation/router"
)

type Engine = contracts.Contract
type Config = routerhttp.Config

func NewHandler(api Engine, config Config) (http.Handler, error) {
	handler, err := routerhttp.NewHandler(api, config)
	if err != nil {
		return nil, err
	}
	return handler.AdminHTTPHandler(), nil
}
