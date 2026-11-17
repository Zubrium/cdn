// Package sitesapi adapts the typed Website contract to the HTTP Sites API.
package sitesapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	website "github.com/Zubrium/cdn/engines/website/presentation"
)

const importPath = "/api/v2/admin/sites/feature-imports"

type Config struct {
	MaxJSONBytes int64
}

type Handler struct {
	website ImportAPI
	cfg     Config
	mux     *http.ServeMux
}

type ImportAPI interface {
	Authenticate(context.Context, string) (context.Context, error)
	BeginFeatureImport(context.Context, website.ImportFeatureRequest) (website.FeatureImport, error)
	GetFeatureImport(context.Context, string) (website.FeatureImport, error)
}

func NewHandler(api ImportAPI, cfg Config) (*Handler, error) {
	if api == nil || cfg.MaxJSONBytes <= 0 || cfg.MaxJSONBytes > 16<<20 {
		return nil, errors.New("invalid Sites API configuration")
	}
	h := &Handler{website: api, cfg: cfg, mux: http.NewServeMux()}
	h.mux.HandleFunc("POST "+importPath, h.beginImport)
	h.mux.HandleFunc("GET "+importPath+"/{id}", h.getImport)
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) beginImport(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var request struct {
		SourceFileID   string `json:"source_file_id"`
		ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	request.SourceFileID = strings.TrimSpace(request.SourceFileID)
	if request.SourceFileID == "" || (request.ExpectedSHA256 != "" && !sha256Pattern.MatchString(request.ExpectedSHA256)) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	job, err := h.website.BeginFeatureImport(ctx, website.ImportFeatureRequest{
		SourceFileID: request.SourceFileID, ExpectedSHA256: strings.ToLower(request.ExpectedSHA256), IdempotencyKey: key,
	})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	w.Header().Set("Location", importPath+"/"+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) getImport(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	job, err := h.website.GetFeatureImport(ctx, id)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")) == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	ctx, err := h.website.Authenticate(r.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
	if err != nil {
		writeEngineError(w, err)
		return nil, false
	}
	return ctx, true
}

var sha256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func validIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("-_.:", char) {
			continue
		}
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, target any) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return website.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return website.ErrTooLarge
		}
		return website.ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return website.ErrInvalid
	}
	return nil
}

func writeEngineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, website.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, website.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, website.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, website.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, website.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large")
	case errors.Is(err, website.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func writeDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, website.ErrTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_request")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
