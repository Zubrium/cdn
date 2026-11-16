// Package settings exposes the sanitized runtime configuration for the admin
// settings section.
package settings

import (
	"net/http"

	settingsfeature "websiteengine/features/settings"
	"websiteengine/pkg/response"
)

// Handler serves the sanitized settings payload.
type Handler struct {
	service *settingsfeature.Service
}

// NewHandler builds a handler for the settings feature.
func NewHandler(service *settingsfeature.Service) *Handler {
	return &Handler{service: service}
}

// Get responds with the sanitized settings payload.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, http.StatusOK, h.service.Get())
}
