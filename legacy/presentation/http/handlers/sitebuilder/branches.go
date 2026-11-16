package sitebuilder

import (
	"github.com/google/uuid"
	"net/http"
	"websiteengine/pkg/principal"
	"websiteengine/pkg/response"
)

func (h *SnapshotsHandler) Branches(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.Branches(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"items": items})
}
func (h *SnapshotsHandler) AssignBranch(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	var in struct {
		SnapshotID      uuid.UUID `json:"snapshotId"`
		Revision        int64     `json:"revision"`
		ContentRevision *int64    `json:"contentRevision"`
	}
	if err := response.DecodeJSON(w, r, &in, h.maxJSON); err != nil {
		response.WriteDecodeError(w, r, err)
		return
	}
	if in.ContentRevision == nil {
		response.Error(w, r, http.StatusBadRequest, "content_revision_required", "Content revision is required to confirm the snapshot assignment")
		return
	}
	actor, _ := principal.FromContext(r.Context())
	record, err := h.service.AssignBranch(r.Context(), actor.TokenID, id, in.SnapshotID, in.Revision, in.ContentRevision)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, 200, record)
}
func (h *SnapshotsHandler) Snapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	record, err := h.service.Snapshot(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	response.JSON(w, 200, record)
}
func (h *SnapshotsHandler) DeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := snapshotID(w, r)
	if !ok {
		return
	}
	if err := h.service.DeleteSnapshot(r.Context(), id); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
