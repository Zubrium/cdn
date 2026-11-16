package sitebuilder

import (
	"net/http"
	"strings"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/principal"
	"websiteengine/pkg/response"
)

func (h *SnapshotsHandler) SnapshotDesignSystem(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	workspace, err := h.service.SnapshotDesignWorkspace(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusOK, workspace)
}
func (h *SnapshotsHandler) SaveSnapshotDesignSystem(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name         string                   `json:"name"`
		DesignSystem sitebuilder.DesignSystem `json:"designSystem"`
	}
	if err := response.DecodeJSON(w, r, &req, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		workspace, err := h.service.SnapshotDesignWorkspace(r.Context(), id)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		name := []rune(workspace.Snapshot.Name)
		if len(name) > 148 {
			name = name[:148]
		}
		req.Name = string(name) + " · дизайн"
	}
	actor, _ := principal.FromContext(r.Context())
	record, err := h.service.CreateSnapshot(r.Context(), actor.TokenID, sitebuilder.CreateSnapshotInput{Name: req.Name, ParentID: &id, DesignSystem: &req.DesignSystem})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, http.StatusCreated, record)
}
