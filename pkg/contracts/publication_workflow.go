package contracts

import (
	"errors"
	"regexp"

	"github.com/google/uuid"
)

// Durable publication workflow operations. Root and child task classes are
// assigned by the static composition registry, never by an operation payload.
const (
	PublicationStartOperationID    = "publication.publish.start"
	PublicationBuildOperationID    = "publication.publish.build"
	PublicationActivateOperationID = "publication.publish.activate"
	PublicationFinalizeOperationID = "publication.publish.finalize"

	PublicationStartContractVersion = 1
	PublicationChildContractVersion = 1

	PublicationWorkflowFormat       = "publication.publish/v1"
	PublicationBuildResultFormat    = "publication.publish.build/v1"
	PublicationActivateResultFormat = "publication.publish.activate/v1"
	PublicationFinalizeResultFormat = "publication.publish.finalize/v1"
)

var (
	ErrInvalidPublicationWorkflow = errors.New("invalid publication workflow payload")
	workflowUUIDPattern           = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	workflowLocalePattern         = regexp.MustCompile(`^[a-z]{2,8}(-[A-Z][a-z]{3})?(-([A-Z]{2}|[0-9]{3}))?(-[A-Za-z0-9]{1,8})*$`)
	workflowKeyPattern            = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,256}$`)
)

// ValidateWorkflowLocale checks a server-owned locale tag without importing an
// engine. Composition canonicalizes configured locales before they enter a
// durable payload; this validation rejects replaced or malformed values.
// ValidWorkflowID checks the strict identifier grammar shared by workflow
// payloads. It is neutral so neither domain package imports an engine.
func ValidWorkflowID(value string) bool {
	return workflowUUIDPattern.MatchString(value)
}

func ValidateWorkflowLocale(value string) error {
	if len(value) == 0 || len(value) > 64 || !workflowLocalePattern.MatchString(value) {
		return ErrInvalidPublicationWorkflow
	}
	return nil
}

// PublicationWorkflowTarget is one server-owned (route, snapshot, locale)
// slot. Router never sees the snapshot; Website never selects the route.
type PublicationWorkflowTarget struct {
	RouteID        string `json:"route_id"`
	PageSnapshotID string `json:"page_snapshot_id"`
	Locale         string `json:"locale"`
}

// PublicationWorkflowPayload is the immutable root-task input. It carries no
// module identity, task class, credential, operation selector or Core handle.
type PublicationWorkflowPayload struct {
	RunID                      string                      `json:"run_id"`
	BranchID                   string                      `json:"branch_id"`
	Locale                     string                      `json:"locale"`
	ContentPublicationRevision int64                       `json:"content_publication_revision"`
	SystemModelRevision        int64                       `json:"system_model_revision"`
	IdempotencyKey             string                      `json:"idempotency_key"`
	Targets                    []PublicationWorkflowTarget `json:"targets"`
}

type PublicationBuildPayload struct {
	RunID                      string `json:"run_id"`
	RouteID                    string `json:"route_id"`
	PageSnapshotID             string `json:"page_snapshot_id"`
	Locale                     string `json:"locale"`
	ContentPublicationRevision int64  `json:"content_publication_revision"`
	SystemModelRevision        int64  `json:"system_model_revision"`
	IdempotencyKey             string `json:"idempotency_key"`
}

type PublicationBuildResult struct {
	Format                 string `json:"format"`
	RunID                  string `json:"run_id"`
	RouteID                string `json:"route_id"`
	Locale                 string `json:"locale"`
	SnapshotID             string `json:"snapshot_id"`
	InputDigest            string `json:"input_digest"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id"`
}

type PublicationActivatePayload struct {
	RunID                  string `json:"run_id"`
	RouteID                string `json:"route_id"`
	Locale                 string `json:"locale"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id"`
	IdempotencyKey         string `json:"idempotency_key"`
}

type PublicationActivateResult struct {
	Format                 string `json:"format"`
	RunID                  string `json:"run_id"`
	RouteID                string `json:"route_id"`
	Locale                 string `json:"locale"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id"`
	Revision               int64  `json:"revision"`
}

type PublicationFinalizePayload struct {
	RunID string `json:"run_id"`
}

type PublicationFinalizeResult struct {
	Format string `json:"format"`
	RunID  string `json:"run_id"`
	State  string `json:"state"`
}

type PublicationStartResult struct {
	Format               string   `json:"format"`
	RunID                string   `json:"run_id"`
	BuildEmissionKeys    []string `json:"build_emission_keys"`
	ActivateEmissionKeys []string `json:"activate_emission_keys"`
	FinalizeEmissionKey  string   `json:"finalize_emission_key"`
}

func ValidatePublicationWorkflowPayload(payload PublicationWorkflowPayload) error {
	if !workflowUUIDPattern.MatchString(payload.RunID) || payload.BranchID == "" ||
		ValidateWorkflowLocale(payload.Locale) != nil || payload.ContentPublicationRevision < 0 ||
		payload.SystemModelRevision < 0 || !workflowKeyPattern.MatchString(payload.IdempotencyKey) ||
		len(payload.Targets) == 0 || len(payload.Targets) > 256 {
		return ErrInvalidPublicationWorkflow
	}
	seen := make(map[string]struct{}, len(payload.Targets))
	for _, target := range payload.Targets {
		if !workflowUUIDPattern.MatchString(target.RouteID) ||
			!workflowUUIDPattern.MatchString(target.PageSnapshotID) || target.Locale != payload.Locale {
			return ErrInvalidPublicationWorkflow
		}
		key := target.RouteID + "\x00" + target.Locale
		if _, duplicate := seen[key]; duplicate {
			return ErrInvalidPublicationWorkflow
		}
		seen[key] = struct{}{}
	}
	return nil
}

func ValidatePublicationBuildPayload(payload PublicationBuildPayload) error {
	if !workflowUUIDPattern.MatchString(payload.RunID) || !workflowUUIDPattern.MatchString(payload.RouteID) ||
		!workflowUUIDPattern.MatchString(payload.PageSnapshotID) || ValidateWorkflowLocale(payload.Locale) != nil ||
		payload.ContentPublicationRevision < 0 || payload.SystemModelRevision < 0 ||
		!workflowKeyPattern.MatchString(payload.IdempotencyKey) {
		return ErrInvalidPublicationWorkflow
	}
	return nil
}

func ValidatePublicationActivatePayload(payload PublicationActivatePayload) error {
	if !workflowUUIDPattern.MatchString(payload.RunID) || !workflowUUIDPattern.MatchString(payload.RouteID) ||
		ValidateWorkflowLocale(payload.Locale) != nil || !workflowUUIDPattern.MatchString(payload.ArtifactManifestFileID) ||
		!workflowKeyPattern.MatchString(payload.IdempotencyKey) {
		return ErrInvalidPublicationWorkflow
	}
	return nil
}

func ValidatePublicationFinalizePayload(payload PublicationFinalizePayload) error {
	if !workflowUUIDPattern.MatchString(payload.RunID) {
		return ErrInvalidPublicationWorkflow
	}
	return nil
}

func PublicationBuildEmissionKey(runID, routeID string) string {
	return "publication.build:" + runID + ":" + routeID
}

func PublicationActivateEmissionKey(runID, routeID string) string {
	return "publication.activate:" + runID + ":" + routeID
}

func PublicationFinalizeEmissionKey(runID string) string {
	return "publication.finalize:" + runID
}

// PublicationArtifactFileID is the server-derived, immutable file request key
// for one publication slot. It is recomputed by build and activation workers;
// no worker can choose a different file or route.
func PublicationArtifactFileID(runID, routeID, locale string) string {
	key := "publication-artifact\x00" + runID + "\x00" + routeID + "\x00" + locale
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(key)).String()
}

func PublicationBuildIdempotencyKey(payload PublicationBuildPayload) string {
	return "pub-build:" + payload.RunID + ":" + payload.RouteID
}

func PublicationActivateIdempotencyKey(payload PublicationActivatePayload) string {
	return "pub-activate:" + payload.RunID + ":" + payload.RouteID
}
