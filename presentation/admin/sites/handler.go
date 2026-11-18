// Package sitesapi adapts the typed Website contract to the HTTP Sites API.
package sitesapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	website "github.com/Zubrium/cdn/engines/website/presentation"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/httpauth"
)

const importPath = "/api/v2/admin/sites/feature-imports"
const apiPrefix = "/api/v2/admin/sites"

type Config struct {
	MaxJSONBytes    int64
	DefaultPageSize int
	MaxPageSize     int
	AllowedOrigins  []string
}

type Handler struct {
	website website.API
	cfg     Config
	auth    *adminhttpauth.Transport
	mux     *http.ServeMux
}

func NewHandler(api website.API, cfg Config) (*Handler, error) {
	if nilInterface(api) || cfg.MaxJSONBytes <= 0 || cfg.MaxJSONBytes > 16<<20 ||
		cfg.DefaultPageSize < 1 || cfg.MaxPageSize < cfg.DefaultPageSize || cfg.MaxPageSize > 500 {
		return nil, errors.New("invalid Sites API configuration")
	}
	auth, err := adminhttpauth.New(cfg.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	h := &Handler{website: api, cfg: cfg, auth: auth, mux: http.NewServeMux()}
	h.mux.HandleFunc("POST "+importPath, h.beginImport)
	h.mux.HandleFunc("GET "+importPath+"/{id}", h.getImport)
	h.mux.HandleFunc("GET "+apiPrefix+"/features", h.listFeatureCatalog)
	h.mux.HandleFunc("GET "+apiPrefix+"/features/{featureID}/versions", h.listFeatureVersions)
	h.mux.HandleFunc("GET "+apiPrefix+"/features/{featureID}/versions/{version}", h.getFeatureVersion)
	h.mux.HandleFunc("GET "+apiPrefix+"/snapshots", h.listSnapshots)
	h.mux.HandleFunc("GET "+apiPrefix+"/snapshots/{snapshotID}", h.getSnapshot)
	h.mux.HandleFunc("GET "+apiPrefix+"/snapshots/{snapshotID}/design-system", h.getSnapshotDesignSystem)
	h.mux.HandleFunc("POST "+apiPrefix+"/snapshots", h.createSnapshot)
	h.mux.HandleFunc("DELETE "+apiPrefix+"/snapshots/{snapshotID}", h.deleteSnapshot)
	h.mux.HandleFunc("PUT "+apiPrefix+"/snapshots/{snapshotID}/features/{featureID}", h.pinFeatureVersion)
	h.mux.HandleFunc("GET "+apiPrefix+"/branches", h.listBranches)
	h.mux.HandleFunc("PUT "+apiPrefix+"/branches/{branchID}/snapshot", h.assignBranch)
	h.mux.HandleFunc("GET "+apiPrefix+"/branches/{branchID}/events", h.listBranchEvents)
	h.mux.HandleFunc("POST "+apiPrefix+"/builds", h.buildSnapshot)
	h.mux.HandleFunc("GET "+apiPrefix+"/builds/{jobID}", h.getBuild)
	h.mux.HandleFunc("POST "+apiPrefix+"/media/derivatives", h.createMediaDerivative)
	h.mux.HandleFunc("GET "+apiPrefix+"/media/jobs/{jobID}", h.getMediaJob)
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
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
	if !validUUID(request.SourceFileID) || (request.ExpectedSHA256 != "" && !sha256Pattern.MatchString(request.ExpectedSHA256)) {
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
	if !validID(job.ID) {
		writeError(w, http.StatusBadGateway, "unavailable")
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
	if !validID(id) {
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

func (h *Handler) listFeatureCatalog(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "cursor", "limit")
	if !valid || len(query["cursor"]) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := parseLimit(query["limit"], h.cfg)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.website.ListFeatureCatalog(ctx, query["cursor"], limit)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) listFeatureVersions(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "cursor", "limit")
	featureID := r.PathValue("featureID")
	if !valid || !validID(featureID) || len(query["cursor"]) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := parseLimit(query["limit"], h.cfg)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.website.ListFeatureVersions(ctx, featureID, query["cursor"], limit)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) getFeatureVersion(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	featureID, version := r.PathValue("featureID"), r.PathValue("version")
	if !validID(featureID) || !validVersion(version) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	value, err := h.website.GetFeatureVersion(ctx, featureID, version)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) getSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	snapshotID := r.PathValue("snapshotID")
	if !validID(snapshotID) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	value, err := h.website.GetSnapshot(ctx, snapshotID)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if value.ID != snapshotID || !validID(value.SiteID) || !validSnapshotName(value.Name) || value.Sequence < 1 || value.Revision < 1 ||
		value.Payload == nil || !validSnapshotPayload(*value.Payload) {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	setSnapshotETag(w, value.Revision)
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) getSnapshotDesignSystem(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	snapshotID := r.PathValue("snapshotID")
	if !validID(snapshotID) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	value, err := h.website.GetSnapshotDesignSystem(ctx, snapshotID)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if value.Snapshot.ID != snapshotID || !validID(value.Snapshot.SiteID) || !validSnapshotName(value.Snapshot.Name) ||
		value.Snapshot.Sequence < 1 || value.Snapshot.Revision < 1 || !validUniqueJSONObject(value.DesignSystem, 4<<20) || !validUniqueJSONArray(value.FeatureTokens, 4<<20) {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	setSnapshotETag(w, value.Snapshot.Revision)
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) listSnapshots(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query, valid := readQuery(r, "site_id", "cursor", "limit")
	if !valid || (query["site_id"] != "" && !validUUID(query["site_id"])) || len(query["cursor"]) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := parseLimit(query["limit"], h.cfg)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.website.ListSnapshots(ctx, query["site_id"], query["cursor"], limit)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if len(page.Items) > limit || len(page.NextCursor) > 1024 {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	for _, snapshot := range page.Items {
		if !validID(snapshot.ID) || !validID(snapshot.SiteID) ||
			(query["site_id"] != "" && snapshot.SiteID != query["site_id"]) ||
			!validSnapshotName(snapshot.Name) || snapshot.Sequence < 1 || snapshot.Revision < 1 || snapshot.State == "" {
			writeError(w, http.StatusBadGateway, "unavailable")
			return
		}
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) createSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || !validIdempotencyKey(key) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var input website.CreateSnapshotRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !validUUID(input.SiteID) || (input.ParentID != "" && !validUUID(input.ParentID)) ||
		(input.ParentID == "" && (input.ExpectedRevision != 0 || input.ExpectedContentRevision != 0)) ||
		(input.ParentID != "" && (input.ExpectedRevision < 1 || input.ExpectedContentRevision < 1)) ||
		!validSnapshotName(input.Name) || !validSnapshotPayload(input.Payload) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	input.IdempotencyKey = key
	value, err := h.website.CreateSnapshot(ctx, input)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if !validID(value.ID) || value.SiteID != input.SiteID || value.ParentID != input.ParentID || value.Name != input.Name || value.Sequence < 1 || value.Revision < 1 ||
		value.Payload == nil || !validSnapshotPayload(*value.Payload) {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	w.Header().Set("Location", apiPrefix+"/snapshots/"+value.ID)
	setSnapshotETag(w, value.Revision)
	writeJSON(w, http.StatusCreated, value)
}

func (h *Handler) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	snapshotID := r.PathValue("snapshotID")
	if !validID(snapshotID) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	revision, status := parseIfMatchRevision(r.Header.Values("If-Match"))
	if status != 0 {
		writeError(w, status, "invalid_revision")
		return
	}
	if err := h.website.DeleteSnapshot(ctx, website.DeleteSnapshotRequest{SnapshotID: snapshotID, ExpectedRevision: revision}); err != nil {
		writeEngineError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validSnapshotName(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 160 {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validSnapshotPayload(payload website.SnapshotPayload) bool {
	return validUniqueJSONObject(payload.Config, 8<<20) && validUniqueJSONObject(payload.Metadata, 1<<20) && validUniqueJSONObject(payload.DesignSystem, 4<<20)
}

func validUniqueJSONObject(raw []byte, maxBytes int) bool {
	trimmed := strings.TrimSpace(string(raw))
	if !utf8.Valid(raw) || len(trimmed) == 0 || len(trimmed) > maxBytes || trimmed[0] != '{' {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	if err := consumeSnapshotJSONValue(decoder, 0); err != nil {
		return false
	}
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

func validUniqueJSONArray(raw []byte, maxBytes int) bool {
	trimmed := strings.TrimSpace(string(raw))
	if !utf8.Valid(raw) || len(trimmed) == 0 || len(trimmed) > maxBytes || trimmed[0] != '[' {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	if err := consumeSnapshotJSONValue(decoder, 0); err != nil {
		return false
	}
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

func consumeSnapshotJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("snapshot JSON nesting exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid snapshot JSON object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate snapshot JSON object key")
			}
			seen[key] = struct{}{}
			if err := consumeSnapshotJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closeToken, err := decoder.Token()
		if err != nil || closeToken != json.Delim('}') {
			return errors.New("invalid snapshot JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeSnapshotJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closeToken, err := decoder.Token()
		if err != nil || closeToken != json.Delim(']') {
			return errors.New("invalid snapshot JSON array")
		}
	default:
		return errors.New("invalid snapshot JSON value")
	}
	return nil
}

func parseIfMatchRevision(values []string) (int64, int) {
	if len(values) == 0 {
		return 0, http.StatusPreconditionRequired
	}
	if len(values) != 1 {
		return 0, http.StatusBadRequest
	}
	value := values[0]
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, http.StatusBadRequest
	}
	raw := value[1 : len(value)-1]
	if raw == "" || raw[0] == '0' {
		return 0, http.StatusBadRequest
	}
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != raw {
		return 0, http.StatusBadRequest
	}
	return revision, 0
}

func setSnapshotETag(w http.ResponseWriter, revision int64) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(revision, 10)+`"`)
}

func (h *Handler) pinFeatureVersion(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var input struct {
		Version          string `json:"version"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	snapshotID, featureID := r.PathValue("snapshotID"), r.PathValue("featureID")
	if !validID(snapshotID) || !validID(featureID) || !validVersion(input.Version) || input.ExpectedRevision < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	value, err := h.website.PinFeatureVersion(ctx, website.PinFeatureVersionRequest{SnapshotID: snapshotID, FeatureID: featureID,
		Version: input.Version, ExpectedRevision: input.ExpectedRevision})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) listBranches(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if _, valid := readQuery(r); !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.website.ListBranches(ctx)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if page.Items == nil {
		page.Items = []website.Branch{}
	}
	seenBranchIDs := make(map[string]struct{}, len(page.Items))
	for index := range page.Items {
		branch := &page.Items[index]
		if branch.CORSOrigins == nil {
			branch.CORSOrigins = []string{}
		}
		if !validUUID(branch.ID) || !validUUID(branch.SiteID) || branch.Revision < 1 || !validBranchName(branch.Name) ||
			!validBranchListenAddress(branch.ListenAddress) || !validBranchCORSOrigins(branch.CORSOrigins) ||
			(branch.SnapshotID != nil && !validUUID(*branch.SnapshotID)) || branch.UpdatedAt.IsZero() {
			writeError(w, http.StatusBadGateway, "unavailable")
			return
		}
		if _, duplicate := seenBranchIDs[branch.ID]; duplicate {
			writeError(w, http.StatusBadGateway, "unavailable")
			return
		}
		seenBranchIDs[branch.ID] = struct{}{}
	}
	writeJSON(w, http.StatusOK, page)
}

func validBranchName(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 100 {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validBranchListenAddress(value string) bool {
	if !utf8.ValidString(value) || value == "" || len(value) > 255 || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	_, portText, err := net.SplitHostPort(value)
	if err != nil || portText == "" {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port > 0 && port <= 65535
}

func validBranchCORSOrigins(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		parsed, err := url.Parse(value)
		if err != nil || !utf8.ValidString(value) || value == "" || len(value) > 512 || value != strings.TrimSpace(value) ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			parsed.Host != strings.ToLower(parsed.Host) || strings.ContainsAny(parsed.Host, "*%,") || strings.HasSuffix(parsed.Host, ":") ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			parsed.RawFragment != "" || parsed.Opaque != "" || value != parsed.Scheme+"://"+parsed.Host {
			return false
		}
		if port := parsed.Port(); port != "" {
			portNumber, err := strconv.Atoi(port)
			if err != nil || portNumber < 1 || portNumber > 65535 ||
				(parsed.Scheme == "http" && portNumber == 80) || (parsed.Scheme == "https" && portNumber == 443) {
				return false
			}
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func (h *Handler) assignBranch(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	branchID := r.PathValue("branchID")
	if !validUUID(branchID) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var input website.AssignBranchRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !validUUID(input.SnapshotID) || input.ExpectedRevision < 1 || input.ExpectedSnapshotRevision < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	input.BranchID = branchID
	branch, err := h.website.AssignBranch(ctx, input)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if branch.ID != branchID || !validUUID(branch.SiteID) || branch.Revision <= input.ExpectedRevision ||
		branch.SnapshotID == nil || *branch.SnapshotID != input.SnapshotID {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(branch.Revision, 10)+`"`)
	writeJSON(w, http.StatusOK, branch)
}

func (h *Handler) listBranchEvents(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	branchID := r.PathValue("branchID")
	query, valid := readQuery(r, "cursor", "limit")
	if !valid || !validUUID(branchID) || len(query["cursor"]) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	limit, valid := parseLimit(query["limit"], h.cfg)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	page, err := h.website.ListBranchEvents(ctx, branchID, query["cursor"], limit)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if len(page.NextCursor) > 1024 {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	if page.Items == nil {
		page.Items = []website.BranchEvent{}
	}
	seenEventIDs := make(map[string]struct{}, len(page.Items))
	for _, event := range page.Items {
		if !validUUID(event.ID) || event.BranchID != branchID || !validUUID(event.SnapshotID) ||
			!validSnapshotName(event.SnapshotName) || event.SnapshotSequence < 1 || event.Action != "assigned" || event.CreatedAt.IsZero() {
			writeError(w, http.StatusBadGateway, "unavailable")
			return
		}
		if _, duplicate := seenEventIDs[event.ID]; duplicate {
			writeError(w, http.StatusBadGateway, "unavailable")
			return
		}
		seenEventIDs[event.ID] = struct{}{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) buildSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var input struct {
		SnapshotID string `json:"snapshot_id"`
	}
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !validID(input.SnapshotID) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	job, err := h.website.BuildSnapshot(ctx, website.BuildSnapshotRequest{SnapshotID: input.SnapshotID, IdempotencyKey: key})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if !validID(job.ID) {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	w.Header().Set("Location", apiPrefix+"/builds/"+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) getBuild(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	jobID := r.PathValue("jobID")
	if !validID(jobID) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	job, err := h.website.GetBuild(ctx, jobID)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) createMediaDerivative(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var input website.CreateDerivativeRequest
	if err := decodeJSON(w, r, h.cfg.MaxJSONBytes, &input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !validID(input.SourceFileID) || !validID(input.FeatureID) || !validID(input.Profile) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	input.IdempotencyKey = key
	job, err := h.website.CreateMediaDerivative(ctx, input)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if !validID(job.ID) {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	w.Header().Set("Location", apiPrefix+"/media/jobs/"+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) getMediaJob(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	jobID := r.PathValue("jobID")
	if !validID(jobID) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	job, err := h.website.GetMediaJob(ctx, jobID)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	credential, err := adminhttpauth.Resolve(r)
	if err != nil {
		if errors.Is(err, adminhttpauth.ErrAmbiguous) {
			writeError(w, http.StatusBadRequest, "ambiguous_authentication")
		} else {
			writeError(w, http.StatusUnauthorized, "unauthorized")
		}
		return nil, false
	}
	var ctx context.Context
	if credential.Session {
		if err := h.auth.ValidateSessionRequest(r); err != nil {
			writeError(w, http.StatusForbidden, "csrf_forbidden")
			return nil, false
		}
		ctx, err = h.website.AuthenticateSession(r.Context(), credential.Value)
	} else {
		ctx, err = h.website.Authenticate(r.Context(), credential.Value)
	}
	if err != nil {
		writeEngineError(w, err)
		return nil, false
	}
	if ctx == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
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
	contentTypes := r.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		return website.ErrInvalid
	}
	mediaType, _, mediaErr := mime.ParseMediaType(contentTypes[0])
	if mediaErr != nil || mediaType != "application/json" {
		return website.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	raw, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(readErr, &tooLarge) {
			return website.ErrTooLarge
		}
		return website.ErrInvalid
	}
	if !validUniqueJSONObject(raw, int(maxBytes)) {
		return website.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
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

func readQuery(r *http.Request, allowed ...string) (map[string]string, bool) {
	allow := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	values := r.URL.Query()
	for key, items := range values {
		if _, ok := allow[key]; !ok || len(items) != 1 {
			return nil, false
		}
	}
	result := make(map[string]string, len(allowed))
	for _, key := range allowed {
		value := values.Get(key)
		if value != strings.TrimSpace(value) {
			return nil, false
		}
		result[key] = value
	}
	return result, true
}

func parseLimit(value string, config Config) (int, bool) {
	if value == "" {
		return config.DefaultPageSize, true
	}
	limit, err := strconv.Atoi(value)
	return limit, err == nil && limit > 0 && limit <= config.MaxPageSize
}

func validID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("_-.", char) {
			continue
		}
		return false
	}
	return true
}

func validUUID(value string) bool {
	return uuidPattern.MatchString(value)
}

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func validVersion(value string) bool {
	return value != "" && len(value) <= 128 && versionPattern.MatchString(value)
}

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]*$`)

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
