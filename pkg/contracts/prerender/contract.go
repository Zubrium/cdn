// Package prerender defines the versioned, trusted wire contract shared by the
// Website build workflow and the prerender worker. No field in this package is
// populated from an HTTP request.
package prerender

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	// TrustedInputVersion pins the semantic meaning of every digest input.
	TrustedInputVersion = "trusted-build-input/v1"
	// PlanVersion is the exact render-plan wire format accepted by this build.
	PlanVersion = "render-plan/v2"
	// ResultFormat and ArtifactFormat pin the packaging contract.
	ResultFormat      = "prerender-result/v1"
	ArtifactFormat    = "prerender-artifact/v1"
	BuildResultFormat = "website.build-result/v2"
	// ArtifactMediaType is the only manifest media type accepted by Core.
	ArtifactMediaType = "application/vnd.zubrium.website.prerender+json"
	// HTMLMediaType is the rendered document media type.
	HTMLMediaType = "text/html; charset=utf-8"
	// RuntimeABI is the only feature runtime ABI that may enter a render plan.
	RuntimeABI = "static-lifecycle"
	// RuntimeAPIVersion pins the browser-side runtime API surface.
	RuntimeAPIVersion = "website-runtime-api/v1"
)

var (
	ErrInvalidInput    = errors.New("invalid trusted prerender input")
	ErrInvalidPlan     = errors.New("invalid prerender plan")
	ErrInvalidManifest = errors.New("invalid prerender artifact manifest")
)

// TrustedInput is the complete immutable input set for one locale-bound
// prerender. It is assembled only from startup configuration and trusted Core
// capabilities; it is never decoded from a visitor or admin request.
type TrustedInput struct {
	Version                    string `json:"version"`
	SnapshotID                 string `json:"snapshot_id"`
	SnapshotRevision           int64  `json:"snapshot_revision"`
	SnapshotDigest             string `json:"snapshot_digest"`
	SystemModelRevision        int64  `json:"system_model_revision"`
	SystemModelDigest          string `json:"system_model_digest"`
	Locale                     string `json:"locale"`
	LocaleCatalogRevision      int64  `json:"locale_catalog_revision"`
	ContentPublicationRevision int64  `json:"content_publication_revision"`
	LocaleSelectionDigest      string `json:"locale_selection_digest"`
	FeaturePackageDigest       string `json:"feature_package_digest"`
	DependencyLockDigest       string `json:"dependency_lock_digest"`
	RuntimeABI                 string `json:"runtime_abi"`
	RuntimeAPIVersion          string `json:"runtime_api_version"`
}

// PagePlan contains the already-escaped server-rendered document. The worker
// never fetches content, follows URLs, or interprets feature code paths.
type PagePlan struct {
	Title    string `json:"title"`
	BodyHTML string `json:"body_html"`
}

// Plan is the exact byte sequence digested by PlanDigest.
type Plan struct {
	Version     string       `json:"version"`
	InputDigest string       `json:"input_digest"`
	Input       TrustedInput `json:"input"`
	Page        PagePlan     `json:"page"`
}

// Artifact describes one immutable Core file produced by a render.
type Artifact struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	FileID    string `json:"file_id,omitempty"`
}

// Manifest is the versioned, verifiable artifact index. It is uploaded to
// Core together with the rendered document and referenced by the task result.
type Manifest struct {
	Format      string      `json:"format"`
	InputDigest string      `json:"input_digest"`
	Locale      string      `json:"locale"`
	Artifacts   []Artifact  `json:"artifacts"`
	Render      RenderProof `json:"render"`
}

// RenderProof records the in-container isolation facts observed before the
// browser was started and after the DOM was captured.
type RenderProof struct {
	Backend          string `json:"backend"`
	Image            string `json:"image"`
	SeccompMode      int    `json:"seccomp_mode"`
	CapEff           string `json:"cap_eff"`
	NoNewPrivs       bool   `json:"no_new_privs"`
	ReadOnlyRootFS   bool   `json:"read_only_root_fs"`
	NetworkDenied    bool   `json:"network_denied"`
	CPUQuota         string `json:"cpu_quota"`
	MemoryLimitBytes int64  `json:"memory_limit_bytes"`
	PIDsLimit        int64  `json:"pids_limit"`
	BrowserVersion   string `json:"browser_version"`
	Locale           string `json:"locale"`
	DOMMarker        string `json:"dom_marker"`
}

// ArtifactSet is the in-memory output of one successful render.
type ArtifactSet struct {
	ManifestJSON []byte
	Document     []byte
	Manifest     Manifest
}

func Digest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return HexDigest(encoded), nil
}

func HexDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// ValidateTrustedInput fails closed for missing, malformed, or unversioned
// inputs. Every component of the digest must be immutable and bounded.
func ValidateTrustedInput(input TrustedInput) error {
	if input.Version != TrustedInputVersion || input.SnapshotID == "" || input.SnapshotRevision < 1 ||
		!isDigest(input.SnapshotDigest) || input.SystemModelRevision < 1 || !isDigest(input.SystemModelDigest) ||
		input.Locale == "" || strings.ContainsAny(input.Locale, "\x00\r\n") || input.LocaleCatalogRevision < 1 ||
		input.ContentPublicationRevision < 0 || !isDigest(input.LocaleSelectionDigest) ||
		!isDigest(input.FeaturePackageDigest) || !isDigest(input.DependencyLockDigest) ||
		input.RuntimeABI != RuntimeABI || input.RuntimeAPIVersion != RuntimeAPIVersion {
		return ErrInvalidInput
	}
	return nil
}

// InputDigest computes the canonical digest of the complete trusted input.
// Map-like values are already reduced to stable digests by the resolver.
func InputDigest(input TrustedInput) (string, error) {
	if err := ValidateTrustedInput(input); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	return HexDigest(encoded), nil
}

func ValidatePlan(plan Plan) error {
	if plan.Version != PlanVersion || !isDigest(plan.InputDigest) ||
		ValidateTrustedInput(plan.Input) != nil || plan.Page.Title == "" || plan.Page.BodyHTML == "" ||
		len(plan.Page.BodyHTML) > 1<<20 {
		return ErrInvalidPlan
	}
	expected, err := InputDigest(plan.Input)
	if err != nil || expected != plan.InputDigest {
		return ErrInvalidPlan
	}
	return nil
}

func MarshalPlan(plan Plan) ([]byte, error) {
	if err := ValidatePlan(plan); err != nil {
		return nil, err
	}
	return json.Marshal(plan)
}

// BuildArtifactSet creates the deterministic document and manifest bytes. The
// manifest checksums are computed over those exact bytes and are verified by
// Core on upload.
func BuildArtifactSet(plan Plan, document []byte, proof RenderProof) (ArtifactSet, error) {
	if err := ValidatePlan(plan); err != nil {
		return ArtifactSet{}, err
	}
	if len(document) == 0 || len(document) > 1<<20 || proof.BrowserVersion == "" || proof.DOMMarker == "" {
		return ArtifactSet{}, ErrInvalidManifest
	}
	documentCopy := append([]byte(nil), document...)
	manifest := Manifest{
		Format:      ArtifactFormat,
		InputDigest: plan.InputDigest,
		Locale:      plan.Input.Locale,
		Artifacts: []Artifact{{
			Path: "index.html", MediaType: HTMLMediaType,
			SHA256: HexDigest(documentCopy), Size: int64(len(documentCopy)),
		}},
		Render: proof,
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return ArtifactSet{}, err
	}
	return ArtifactSet{ManifestJSON: encoded, Document: documentCopy, Manifest: manifest}, nil
}

// VerifyManifest performs a local structural check of an artifact manifest.
// It never activates an artifact; Core-only storage and checksum verification
// remain the authority for bytes.
func VerifyManifest(raw []byte, expectedInputDigest string) (Manifest, error) {
	var manifest Manifest
	if len(raw) == 0 || len(raw) > 1<<20 || !isDigest(expectedInputDigest) {
		return Manifest{}, ErrInvalidManifest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, ErrInvalidManifest
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Manifest{}, ErrInvalidManifest
	}
	if manifest.Format != ArtifactFormat || manifest.InputDigest != expectedInputDigest ||
		manifest.Locale == "" || len(manifest.Artifacts) == 0 || len(manifest.Artifacts) > 16 {
		return Manifest{}, ErrInvalidManifest
	}
	if proof := manifest.Render; proof.Backend != "docker-linux" || proof.Image == "" ||
		proof.SeccompMode < 2 || !strings.EqualFold(proof.CapEff, "0000000000000000") || !proof.NoNewPrivs ||
		!proof.ReadOnlyRootFS || !proof.NetworkDenied || proof.CPUQuota == "" ||
		proof.MemoryLimitBytes <= 0 || proof.PIDsLimit <= 0 || proof.BrowserVersion == "" ||
		proof.Locale != manifest.Locale || proof.DOMMarker == "" {
		return Manifest{}, ErrInvalidManifest
	}
	seen := make(map[string]struct{}, len(manifest.Artifacts))
	htmlCount := 0
	for _, artifact := range manifest.Artifacts {
		if artifact.Path == "" || artifact.MediaType == "" || artifact.Size <= 0 || !isDigest(artifact.SHA256) {
			return Manifest{}, ErrInvalidManifest
		}
		if _, duplicate := seen[artifact.Path]; duplicate {
			return Manifest{}, ErrInvalidManifest
		}
		seen[artifact.Path] = struct{}{}
		if strings.HasPrefix(artifact.MediaType, "text/html") {
			htmlCount++
		}
	}
	if htmlCount != 1 {
		return Manifest{}, ErrInvalidManifest
	}
	return manifest, nil
}

// MergeInputDigests reduces an ordered set of named digests without depending
// on map iteration order.
func MergeInputDigests(values map[string]string) (string, error) {
	if len(values) == 0 {
		return "", ErrInvalidInput
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if key == "" || !isDigest(value) {
			return "", ErrInvalidInput
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
	}, 0, len(keys))
	for _, key := range keys {
		items = append(items, struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		}{Name: key, Digest: values[key]})
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("encode digest set: %w", err)
	}
	return HexDigest(encoded), nil
}

func isDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
