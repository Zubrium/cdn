package contracts

import (
	"encoding/json"
	"errors"
	"regexp"
)

var ErrInvalidContentProjectionRequest = errors.New("invalid content projection request")

var contentProjectionLocalePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

// ContentProjection is the transport-neutral projection of Website's ready
// page snapshot. It contains no engine type, Core identity or storage handle.
type ContentProjection struct {
	ContentModel ContentModelProjection  `json:"content_model"`
	Page         PagePlacementProjection `json:"page_placement_projection"`
	Locale       string                  `json:"locale"`
}

type ContentModelProjection struct {
	Version       int             `json:"version"`
	ModelRevision int64           `json:"model_revision"`
	Digest        string          `json:"digest"`
	DataModel     json.RawMessage `json:"data_model"`
}

type PagePlacementProjection struct {
	Version        int                `json:"version"`
	PageSnapshotID string             `json:"page_snapshot_id"`
	PageID         string             `json:"page_id"`
	Revision       int64              `json:"revision"`
	Digest         string             `json:"digest"`
	State          string             `json:"state"`
	Placements     []ContentPlacement `json:"placements"`
}

type ContentPlacement struct {
	ID          string                  `json:"id"`
	PageID      string                  `json:"page_id"`
	PageName    string                  `json:"page_name"`
	FeatureKey  string                  `json:"feature_key"`
	FeatureName string                  `json:"feature_name"`
	Version     string                  `json:"version"`
	Inputs      []ContentPlacementInput `json:"inputs"`
}

type ContentPlacementInput struct {
	InputKey string                  `json:"input_key"`
	Kind     string                  `json:"kind"`
	EntityID string                  `json:"entity_id"`
	Fields   []ContentPlacementField `json:"fields"`
	Lookup   *ContentPlacementLookup `json:"lookup,omitempty"`
}

type ContentPlacementField struct {
	InputPath   string `json:"input_path"`
	FieldID     string `json:"field_id"`
	MediaAccept string `json:"media_accept,omitempty"`
}

type ContentPlacementLookup struct {
	Argument string `json:"argument"`
	FieldID  string `json:"field_id"`
}

type ContentProjectionRequest struct {
	PageSnapshotID    string `json:"page_snapshot_id"`
	Locale            string `json:"locale"`
	AllowProvisioning bool   `json:"allow_provisioning"`
}

type ContentProjectionResponse struct {
	Format     string            `json:"format"`
	Projection ContentProjection `json:"projection"`
}

type ContentBuildProjectionRequest struct {
	PageSnapshotID string `json:"page_snapshot_id"`
	Locale         string `json:"locale"`
}

type ContentBuildProjectionResponse struct {
	PublicationRevision int64  `json:"publication_revision"`
	SelectionDigest     string `json:"selection_digest"`
}

type ContentLocaleCatalogRequest struct{}

type ContentLocaleCatalogResponse struct {
	Revision int64    `json:"revision"`
	Locales  []string `json:"locales"`
}

const (
	// Root workflow operations. They are started only by the composition-owned
	// clients and validate server-owned payload before declaring children.
	ContentProjectionOperationID = "content-bridge.projection-read"
	ContentBuildOperationID      = "content-bridge.build-read"
	ContentLocaleOperationID     = "content-bridge.locale-read"
	ContentBridgeRootVersion     = 1

	// Child task operations. Each is claimed by exactly one statically
	// registered class-scoped worker pool.
	WebsiteProjectionOperationID      = "content-bridge.website-projection"
	ContentBuildResolveOperationID    = "content-bridge.content-build"
	ContentLocaleReadOperationID      = "content-bridge.content-locale"
	ContentBuildProjectionOperationID = "content-bridge.build-projection"
	ContentBridgeChildVersion         = 1

	ContentProjectionWorkflowFormat = "content-bridge.projection/v1"
	ContentBuildWorkflowFormat      = "content-bridge.build/v1"
	ContentLocaleWorkflowFormat     = "content-bridge.locale/v1"
	WebsiteProjectionResultFormat   = "website.content-projection/v1"
)

// ContentProjectionWorkflowPayload is the immutable root-task input. A caller
// can select only a canonical page snapshot and a configured locale; operation,
// owner, class and worker identity stay server-side.
type ContentProjectionWorkflowPayload struct {
	PageSnapshotID    string `json:"page_snapshot_id"`
	Locale            string `json:"locale"`
	AllowProvisioning bool   `json:"allow_provisioning"`
}

type ContentBuildWorkflowPayload struct {
	PageSnapshotID string `json:"page_snapshot_id"`
	Locale         string `json:"locale"`
}

type ContentLocaleWorkflowPayload struct{}

// ContentBridgeRootResult carries only the declared child emission identity,
// so a reader cannot choose a task by ID or substitute an unrelated result.
type ContentBridgeRootResult struct {
	Format                string `json:"format"`
	EmissionKey           string `json:"emission_key"`
	ProjectionEmissionKey string `json:"projection_emission_key,omitempty"`
}

var contentBridgeEmissionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)

func ValidContentBridgeEmissionKey(value string) bool {
	return contentBridgeEmissionPattern.MatchString(value)
}

func ContentProjectionEmissionKey(pageSnapshotID, locale string) string {
	return "content-bridge-projection:" + pageSnapshotID + ":" + locale
}

func ContentBuildEmissionKey(pageSnapshotID, locale string) string {
	return "content-bridge-build:" + pageSnapshotID + ":" + locale
}

func ContentBuildProjectionEmissionKey(pageSnapshotID, locale string) string {
	return "content-bridge-build-projection:" + pageSnapshotID + ":" + locale
}

const ContentLocaleEmissionKey = "content-bridge-locale:catalog"

func ValidateContentProjectionWorkflowPayload(payload ContentProjectionWorkflowPayload) error {
	if !validCanonicalUUID(payload.PageSnapshotID) || !contentProjectionLocalePattern.MatchString(payload.Locale) {
		return ErrInvalidContentProjectionRequest
	}
	return nil
}

func ValidateBuildWorkflowPayload(payload ContentBuildWorkflowPayload) error {
	return validateContentProjectionRequest(ContentProjectionRequest{
		PageSnapshotID: payload.PageSnapshotID, Locale: payload.Locale,
	}, false)
}

// ValidateWebsiteProjectionPayload validates the server-owned child payload.
// AllowProvisioning is set by composition, never by a request, so it is not a
// caller privilege flag at this boundary.
func ValidateWebsiteProjectionPayload(request ContentProjectionRequest) error {
	if !validCanonicalUUID(request.PageSnapshotID) || !contentProjectionLocalePattern.MatchString(request.Locale) {
		return ErrInvalidContentProjectionRequest
	}
	return nil
}

func ValidateLocaleWorkflowPayload(ContentLocaleWorkflowPayload) error { return nil }

func validCanonicalUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range []byte(value) {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
				return false
			}
		}
	}
	return value[14] == '1' || value[14] == '2' || value[14] == '3' || value[14] == '4' ||
		value[14] == '5' || value[14] == '6' || value[14] == '7' || value[14] == '8'
}

func validateContentProjectionRequest(request ContentProjectionRequest, allowProvisioning bool) error {
	if !validCanonicalUUID(request.PageSnapshotID) ||
		!contentProjectionLocalePattern.MatchString(request.Locale) ||
		(request.AllowProvisioning && !allowProvisioning) {
		return ErrInvalidContentProjectionRequest
	}
	return nil
}
