package fileapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const apiPrefix = "/api/v2/files"

type Handler struct {
	loader Loader
	cfg    Config
	mux    *http.ServeMux
}

func NewHandler(loader Loader, cfg Config) (*Handler, error) {
	if loader == nil {
		return nil, errors.New("File API Loader Engine is required")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	h := &Handler{loader: loader, cfg: cfg, mux: http.NewServeMux()}
	h.routes()
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) routes() {
	h.mux.HandleFunc("POST "+apiPrefix+"/uploads", h.beginUpload)
	h.mux.HandleFunc("GET "+apiPrefix+"/uploads/{id}", h.uploadStatus)
	h.mux.HandleFunc("PUT "+apiPrefix+"/uploads/{id}/parts/{number}", h.uploadPart)
	h.mux.HandleFunc("POST "+apiPrefix+"/uploads/{id}/commit", h.commitUpload)
	h.mux.HandleFunc("DELETE "+apiPrefix+"/uploads/{id}", h.abortUpload)
	h.mux.HandleFunc("GET "+apiPrefix, h.listFiles)
	h.mux.HandleFunc("GET "+apiPrefix+"/objects/{id}", h.getFile)
	h.mux.HandleFunc("PATCH "+apiPrefix+"/objects/{id}", h.updateFile)
	h.mux.HandleFunc("DELETE "+apiPrefix+"/objects/{id}", h.deleteFile)
	h.mux.HandleFunc("GET "+apiPrefix+"/objects/{id}/content", h.downloadFile)
	h.mux.HandleFunc("HEAD "+apiPrefix+"/objects/{id}/content", h.headFile)
	h.mux.HandleFunc("PUT "+apiPrefix+"/objects/{id}/groups/{group}", h.addGroup)
	h.mux.HandleFunc("DELETE "+apiPrefix+"/objects/{id}/groups/{group}", h.removeGroup)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	credential, ok := bearerCredential(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Valid Core credentials are required")
		return nil, false
	}
	ctx, err := h.loader.Authenticate(r.Context(), credential)
	if err != nil {
		writeLoaderError(w, err, false)
		return nil, false
	}
	if ctx == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The request could not be completed")
		return nil, false
	}
	return ctx, true
}

func bearerCredential(value string) (string, bool) {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func (h *Handler) beginUpload(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "A valid Idempotency-Key is required")
		return
	}
	var input BeginUploadInput
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeRequestError(w, err)
		return
	}
	input.IdempotencyKey = key
	if err := h.validateUploadInput(&input); err != nil {
		writeRequestError(w, err)
		return
	}
	upload, err := h.loader.BeginUpload(ctx, input)
	if err != nil {
		writeLoaderError(w, err, false)
		return
	}
	w.Header().Set("Location", apiPrefix+"/uploads/"+upload.ID)
	writeJSON(w, http.StatusCreated, upload)
}

func (h *Handler) validateUploadInput(input *BeginUploadInput) error {
	if input.Size > h.cfg.MaxFileBytes {
		return ErrTooLarge
	}
	if !validName(input.Name) || !validContentType(input.ContentType) || input.Size < 0 || !validSHA256(input.SHA256) {
		return ErrInvalid
	}
	if len(input.Groups) == 0 || len(input.Groups) > h.cfg.MaxGroupsPerFile {
		return ErrInvalid
	}
	seen := make(map[string]struct{}, len(input.Groups))
	for _, group := range input.Groups {
		if !validGroupKey(group) {
			return ErrInvalid
		}
		if _, exists := seen[group]; exists {
			return ErrInvalid
		}
		seen[group] = struct{}{}
	}
	if err := validateAttributes(input.Attributes, h.cfg.MaxAttributeBytes); err != nil {
		return err
	}
	input.SHA256 = strings.ToLower(input.SHA256)
	input.Attributes = cloneAttributes(input.Attributes)
	input.Groups = append([]string(nil), input.Groups...)
	return nil
}

func (h *Handler) uploadStatus(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		writeRequestError(w, ErrInvalid)
		return
	}
	upload, err := h.loader.UploadStatus(ctx, id)
	if err != nil {
		writeLoaderError(w, err, true)
		return
	}
	writeJSON(w, http.StatusOK, upload)
}

func (h *Handler) uploadPart(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	part, err := strconv.Atoi(r.PathValue("number"))
	if !validUUID(id) || err != nil || part < 1 || part > h.cfg.MaxUploadParts {
		writeRequestError(w, ErrInvalid)
		return
	}
	if r.ContentLength < 1 {
		writeError(w, http.StatusLengthRequired, "content_length_required", "Each upload part must declare its byte length")
		return
	}
	if r.ContentLength > h.cfg.MaxPartBytes {
		writeRequestError(w, ErrTooLarge)
		return
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/octet-stream" || !validSHA256(r.Header.Get("X-Content-SHA256")) {
		writeRequestError(w, ErrInvalid)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxPartBytes)
	defer r.Body.Close()
	upload, err := h.loader.PutUploadPart(ctx, id, part, r.ContentLength, strings.ToLower(r.Header.Get("X-Content-SHA256")), r.Body)
	if err != nil {
		writeLoaderError(w, err, true)
		return
	}
	writeJSON(w, http.StatusOK, upload)
}

func (h *Handler) commitUpload(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		writeRequestError(w, ErrInvalid)
		return
	}
	file, err := h.loader.CommitUpload(ctx, id)
	if err != nil {
		writeLoaderError(w, err, true)
		return
	}
	w.Header().Set("Location", apiPrefix+"/objects/"+file.ID)
	setETag(w, file)
	writeJSON(w, http.StatusCreated, file)
}

func (h *Handler) abortUpload(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		writeRequestError(w, ErrInvalid)
		return
	}
	if err := h.loader.AbortUpload(ctx, id); err != nil {
		writeLoaderError(w, err, true)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listFiles(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, ok := h.listQuery(w, r)
	if !ok {
		return
	}
	page, err := h.loader.ListFiles(ctx, query)
	if err != nil {
		writeLoaderError(w, err, false)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items      []File `json:"items"`
		NextCursor string `json:"nextCursor,omitempty"`
	}{Items: page.Items, NextCursor: page.NextCursor})
}

func (h *Handler) getFile(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		writeLoaderError(w, ErrNotFound, true)
		return
	}
	file, err := h.loader.StatFile(ctx, id)
	if err != nil {
		writeLoaderError(w, err, true)
		return
	}
	setETag(w, file)
	writeJSON(w, http.StatusOK, file)
}

func (h *Handler) updateFile(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		writeLoaderError(w, ErrNotFound, true)
		return
	}
	revision, ok := expectedRevision(w, r, id)
	if !ok {
		return
	}
	var patch FileMetadataPatch
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &patch); err != nil {
		writeRequestError(w, err)
		return
	}
	if err := validatePatch(patch, h.cfg.MaxAttributeBytes); err != nil {
		writeRequestError(w, err)
		return
	}
	file, err := h.loader.UpdateFile(ctx, id, revision, patch)
	if err != nil {
		writeLoaderError(w, err, true)
		return
	}
	setETag(w, file)
	writeJSON(w, http.StatusOK, file)
}

func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		writeLoaderError(w, ErrNotFound, true)
		return
	}
	revision, ok := expectedRevision(w, r, id)
	if !ok {
		return
	}
	if err := h.loader.DeleteFile(ctx, id, revision); err != nil {
		writeLoaderError(w, err, true)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) addGroup(w http.ResponseWriter, r *http.Request) {
	h.changeGroup(w, r, true)
}

func (h *Handler) removeGroup(w http.ResponseWriter, r *http.Request) {
	h.changeGroup(w, r, false)
}

func (h *Handler) changeGroup(w http.ResponseWriter, r *http.Request, add bool) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id, group := r.PathValue("id"), r.PathValue("group")
	if !validUUID(id) || !validGroupKey(group) {
		writeLoaderError(w, ErrNotFound, true)
		return
	}
	revision, ok := expectedRevision(w, r, id)
	if !ok {
		return
	}
	var file File
	var err error
	if add {
		file, err = h.loader.AddFileToGroup(ctx, id, group, revision)
	} else {
		file, err = h.loader.RemoveFileFromGroup(ctx, id, group, revision)
	}
	if err != nil {
		writeLoaderError(w, err, true)
		return
	}
	setETag(w, file)
	writeJSON(w, http.StatusOK, file)
}

func (h *Handler) listQuery(w http.ResponseWriter, r *http.Request) (ListQuery, bool) {
	values := r.URL.Query()
	if len(values.Get("q")) > 255 || len(values.Get("cursor")) > 2048 {
		writeRequestError(w, ErrInvalid)
		return ListQuery{}, false
	}
	limit := h.cfg.DefaultPageSize
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > h.cfg.MaxPageSize {
			writeRequestError(w, ErrInvalid)
			return ListQuery{}, false
		}
		limit = parsed
	}
	group := values.Get("group")
	if group != "" && !validGroupKey(group) {
		writeRequestError(w, ErrInvalid)
		return ListQuery{}, false
	}
	return ListQuery{Group: group, Query: values.Get("q"), Cursor: values.Get("cursor"), Limit: limit}, true
}

func (h *Handler) downloadFile(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		writeLoaderError(w, ErrNotFound, true)
		return
	}
	var body io.ReadCloser
	var file File
	var start, end int64
	partial := false
	var err error
	if rawRange := r.Header.Get("Range"); rawRange != "" {
		file, err = h.loader.StatFile(ctx, id)
		if err == nil {
			start, end, err = parseByteRange(rawRange, file.Size)
		}
		if err == nil {
			body, file, err = h.loader.ReadFileRange(ctx, id, start, end)
			partial = err == nil
		}
	} else {
		body, file, err = h.loader.ReadFile(ctx, id)
		if err == nil {
			start, end = 0, file.Size-1
		}
	}
	if err != nil {
		if errors.Is(err, errInvalidRange) {
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(file.Size, 10))
			writeError(w, http.StatusRequestedRangeNotSatisfiable, "invalid_range", "The requested byte range is not available")
			return
		}
		writeLoaderError(w, err, true)
		return
	}
	if body == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The request could not be completed")
		return
	}
	defer body.Close()
	setDownloadHeaders(w, file)
	if partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.Size))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
		w.WriteHeader(http.StatusOK)
	}
	_, _ = io.Copy(w, body)
}

func (h *Handler) headFile(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		writeLoaderError(w, ErrNotFound, true)
		return
	}
	file, err := h.loader.StatFile(ctx, id)
	if err != nil {
		writeLoaderError(w, err, true)
		return
	}
	start, end := int64(0), file.Size-1
	partial := false
	if rawRange := r.Header.Get("Range"); rawRange != "" {
		start, end, err = parseByteRange(rawRange, file.Size)
		if err != nil {
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(file.Size, 10))
			writeError(w, http.StatusRequestedRangeNotSatisfiable, "invalid_range", "The requested byte range is not available")
			return
		}
		partial = true
	}
	setDownloadHeaders(w, file)
	if partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.Size))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
	w.WriteHeader(http.StatusOK)
}

func expectedRevision(w http.ResponseWriter, r *http.Request, fileID string) (int64, bool) {
	return expectedResourceRevision(w, r, fileID, "file")
}

func expectedResourceRevision(w http.ResponseWriter, r *http.Request, resourceID, resourceName string) (int64, bool) {
	value := r.Header.Get("If-Match")
	if value == "" {
		writeError(w, http.StatusPreconditionRequired, "revision_required", "If-Match with the current "+resourceName+" ETag is required")
		return 0, false
	}
	if len(value) < 5 || value[0] != '"' || value[len(value)-1] != '"' {
		writeRequestError(w, ErrInvalid)
		return 0, false
	}
	inner := value[1 : len(value)-1]
	marker := strings.LastIndex(inner, "-r")
	if marker < 1 {
		writeRequestError(w, ErrInvalid)
		return 0, false
	}
	revision, err := strconv.ParseInt(inner[marker+2:], 10, 64)
	if err != nil || revision < 1 || !validUUID(inner[:marker]) {
		writeRequestError(w, ErrInvalid)
		return 0, false
	}
	if inner[:marker] != resourceID {
		writeError(w, http.StatusPreconditionFailed, "revision_mismatch", "If-Match does not identify this "+resourceName)
		return 0, false
	}
	return revision, true
}

func setETag(w http.ResponseWriter, file File) {
	w.Header().Set("ETag", fmt.Sprintf("\"%s-r%d\"", file.ID, file.Revision))
}

func setDownloadHeaders(w http.ResponseWriter, file File) {
	contentType := file.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "private, no-store")
	setETag(w, file)
}

var errInvalidRange = errors.New("invalid byte range")

func parseByteRange(value string, size int64) (int64, int64, error) {
	if size <= 0 || !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return 0, 0, errInvalidRange
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, errInvalidRange
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, errInvalidRange
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, nil
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, errInvalidRange
	}
	end := size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, errInvalidRange
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, destination any) error {
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		return ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return ErrTooLarge
		}
		return ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return ErrTooLarge
		}
		return ErrInvalid
	}
	return nil
}

func validatePatch(patch FileMetadataPatch, maxAttributes int64) error {
	if patch.Name == nil && patch.ContentType == nil && patch.Attributes == nil {
		return ErrInvalid
	}
	if patch.Name != nil && !validName(*patch.Name) {
		return ErrInvalid
	}
	if patch.ContentType != nil && !validContentType(*patch.ContentType) {
		return ErrInvalid
	}
	if patch.Attributes != nil {
		return validateAttributes(*patch.Attributes, maxAttributes)
	}
	return nil
}

func validateAttributes(attributes map[string]string, maxBytes int64) error {
	if len(attributes) > 64 {
		return ErrInvalid
	}
	encoded, err := json.Marshal(attributes)
	if err != nil {
		return ErrInvalid
	}
	if int64(len(encoded)) > maxBytes {
		return ErrTooLarge
	}
	for key, value := range attributes {
		if key == "" || len(key) > 64 || !utf8.ValidString(key) || !utf8.ValidString(value) {
			return ErrInvalid
		}
		for _, r := range key {
			if unicode.IsControl(r) || r == '/' || r == '\\' {
				return ErrInvalid
			}
		}
		for _, r := range value {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return ErrInvalid
			}
		}
	}
	return nil
}

func validName(value string) bool {
	if value == "" || len(value) > 255 || strings.TrimSpace(value) != value || !utf8.ValidString(value) || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func validContentType(value string) bool {
	if value == "" || len(value) > 255 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.Contains(mediaType, "/")
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validGroupKey(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_' || r == '.')) {
			continue
		}
		return false
	}
	return true
}

func validIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_.:", r)) {
			return false
		}
	}
	return true
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}

func cloneAttributes(attributes map[string]string) map[string]string {
	if attributes == nil {
		return nil
	}
	copy := make(map[string]string, len(attributes))
	for key, value := range attributes {
		copy[key] = value
	}
	return copy
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeRequestError(w http.ResponseWriter, err error) {
	writeLoaderError(w, err, false)
}

func writeLoaderError(w http.ResponseWriter, err error, hideForbidden bool) {
	switch {
	case errors.Is(err, ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", "Valid Core credentials are required")
	case errors.Is(err, ErrForbidden) && hideForbidden:
		writeError(w, http.StatusNotFound, "not_found", "The requested resource was not found")
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "The operation is not allowed")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "The requested resource was not found")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "The resource changed or the request conflicts with its current state")
	case errors.Is(err, ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "The request exceeds the configured limit")
	case errors.Is(err, ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request", "The request is invalid")
	case errors.Is(err, ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "The file service is temporarily unavailable")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout", "The file service did not respond in time")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "The request could not be completed")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
