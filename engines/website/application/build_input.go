package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
	"html"
	"sort"
	"strings"
)

// ContentBuildProjection is the narrow, trusted Content snapshot used to pin a
// locale-bound build. It is resolved from Core capabilities, never from a
// request.
type ContentBuildProjection struct {
	PublicationRevision int64  `json:"publication_revision"`
	SelectionDigest     string `json:"selection_digest"`
}

// ContentBuildProjectionResolver supplies the Content half of a trusted build
// input. Composition owns the authoritative implementation.
type ContentBuildProjectionResolver interface {
	ResolveContentBuildProjection(context.Context, string, string) (ContentBuildProjection, error)
}

// PinnedContentBuildProjectionResolver can resolve the Content projection for
// one already published revision instead of whatever happens to be current.
type PinnedContentBuildProjectionResolver interface {
	ResolveContentBuildProjectionPinned(context.Context, string, string, int64) (ContentBuildProjection, error)
}

// BuildInputResolver resolves the complete versioned input set for one
// locale-bound prerender. It is a required trusted capability.
type BuildInputResolver interface {
	ResolveBuildInput(context.Context, websiteapi.Snapshot, string, int64) (prerendercontract.TrustedInput, error)
}

// PinnedBuildInputResolver pins the Content half of a build to an already
// published revision. Publication uses it so a durable trigger cannot build
// from a newer or older publication than the one it represents.
type PinnedBuildInputResolver interface {
	ResolveBuildInputPinned(context.Context, websiteapi.Snapshot, string, int64, int64) (prerendercontract.TrustedInput, error)
}

// ModuleBuildInputResolver resolves build inputs exclusively from registered
// Core module-data records and the trusted Content projection capability.
type ModuleBuildInputResolver struct {
	store     RecordStore
	content   ContentBuildProjectionResolver
	imageHash string
}

// NewModuleBuildInputResolver fails closed unless every capability is present.
func NewModuleBuildInputResolver(store RecordStore, content ContentBuildProjectionResolver, imageDigest string) (*ModuleBuildInputResolver, error) {
	if !validInterface(store) || !validInterface(content) {
		return nil, errConfigInvalid
	}
	imageDigest = strings.TrimSpace(imageDigest)
	if at := strings.LastIndex(imageDigest, "@"); at >= 0 {
		imageDigest = imageDigest[at+1:]
	}
	if !strings.HasPrefix(imageDigest, "sha256:") || len(imageDigest) != len("sha256:")+sha256.Size*2 {
		return nil, errConfigInvalid
	}
	for _, r := range imageDigest[len("sha256:"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return nil, errConfigInvalid
		}
	}
	return &ModuleBuildInputResolver{store: store, content: content, imageHash: imageDigest}, nil
}

func (r *ModuleBuildInputResolver) ResolveBuildInput(ctx context.Context, snapshot websiteapi.Snapshot, locale string, catalogRevision int64) (prerendercontract.TrustedInput, error) {
	return r.ResolveBuildInputPinned(ctx, snapshot, locale, catalogRevision, 0)
}

func (r *ModuleBuildInputResolver) ResolveBuildInputPinned(ctx context.Context, snapshot websiteapi.Snapshot, locale string, catalogRevision, expectedContentRevision int64) (prerendercontract.TrustedInput, error) {
	if r == nil || !validInterface(r.store) || !validInterface(r.content) || ctx == nil || snapshot.Payload == nil ||
		len(snapshot.Payload.Config) == 0 || !validBuildLocale(locale) || catalogRevision < 1 || expectedContentRevision < 0 {
		return prerendercontract.TrustedInput{}, websiteapi.ErrUnavailable
	}
	if snapshot.State != "ready" || snapshot.Revision < 1 || !sha256HexPattern.MatchString(snapshot.Digest) {
		return prerendercontract.TrustedInput{}, websiteapi.ErrConflict
	}
	modelRecord, err := r.store.Get(ctx, contentModelKey())
	if err != nil {
		return prerendercontract.TrustedInput{}, mapCoreError(err)
	}
	model, err := decodeContentModelRecord(modelRecord)
	if err != nil || model.Digest == "" {
		return prerendercontract.TrustedInput{}, websiteapi.ErrUnavailable
	}
	var projection ContentBuildProjection
	if expectedContentRevision > 0 {
		if pinned, ok := r.content.(PinnedContentBuildProjectionResolver); ok {
			projection, err = pinned.ResolveContentBuildProjectionPinned(ctx, snapshot.ID, locale, expectedContentRevision)
		} else {
			projection, err = r.content.ResolveContentBuildProjection(ctx, snapshot.ID, locale)
		}
	} else {
		projection, err = r.content.ResolveContentBuildProjection(ctx, snapshot.ID, locale)
	}
	if err != nil || projection.PublicationRevision < 0 || !sha256HexPattern.MatchString(projection.SelectionDigest) {
		return prerendercontract.TrustedInput{}, websiteapi.ErrUnavailable
	}
	if expectedContentRevision > 0 && projection.PublicationRevision != expectedContentRevision {
		return prerendercontract.TrustedInput{}, websiteapi.ErrConflict
	}
	featureDigest, lockDigest, err := r.resolveFeaturePins(ctx, snapshot.FeaturePins)
	if err != nil {
		return prerendercontract.TrustedInput{}, err
	}
	input := prerendercontract.TrustedInput{
		Version:                    prerendercontract.TrustedInputVersion,
		SnapshotID:                 snapshot.ID,
		SnapshotRevision:           snapshot.Revision,
		SnapshotDigest:             snapshot.Digest,
		SystemModelRevision:        model.ModelRevision,
		SystemModelDigest:          model.Digest,
		Locale:                     locale,
		LocaleCatalogRevision:      catalogRevision,
		ContentPublicationRevision: projection.PublicationRevision,
		LocaleSelectionDigest:      projection.SelectionDigest,
		FeaturePackageDigest:       featureDigest,
		DependencyLockDigest:       lockDigest,
		RuntimeABI:                 prerendercontract.RuntimeABI,
		RuntimeAPIVersion:          prerendercontract.RuntimeAPIVersion,
	}
	if err := prerendercontract.ValidateTrustedInput(input); err != nil {
		return prerendercontract.TrustedInput{}, websiteapi.ErrUnavailable
	}
	return input, nil
}

func (r *ModuleBuildInputResolver) resolveFeaturePins(ctx context.Context, pins map[string]string) (string, string, error) {
	if len(pins) == 0 {
		return "", "", websiteapi.ErrUnavailable
	}
	packageDigests := make(map[string]string, len(pins))
	lockDigests := make(map[string]string, len(pins))
	names := make([]string, 0, len(pins))
	for name := range pins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !identifierPattern.MatchString(name) || !featureVersionPattern.MatchString(pins[name]) {
			return "", "", websiteapi.ErrUnavailable
		}
		record, err := r.store.Get(ctx, websiterepo.FeatureVersionKey(name, pins[name]))
		if err != nil {
			return "", "", mapCoreError(err)
		}
		var stored struct {
			RecordType string            `json:"record_type"`
			Manifest   json.RawMessage   `json:"manifest"`
			Files      map[string]string `json:"files"`
			Digest     string            `json:"digest"`
			State      string            `json:"state"`
		}
		if record.PayloadVersion != 1 || json.Unmarshal(record.Payload, &stored) != nil || stored.RecordType != "feature_version" ||
			stored.State != "ready" || !sha256HexPattern.MatchString(stored.Digest) || len(stored.Manifest) == 0 ||
			len(stored.Files) == 0 {
			return "", "", websiteapi.ErrUnavailable
		}
		sum := sha256.New()
		_, _ = sum.Write(stored.Manifest)
		for _, file := range sortedValues(stored.Files) {
			_, _ = sum.Write([]byte(file))
		}
		packageDigests[name] = hex.EncodeToString(sum.Sum(nil))
		lockBytes, err := json.Marshal(struct {
			FeatureID string            `json:"feature_id"`
			Version   string            `json:"version"`
			Digest    string            `json:"digest"`
			Files     map[string]string `json:"files"`
		}{name, pins[name], stored.Digest, stored.Files})
		if err != nil {
			return "", "", websiteapi.ErrUnavailable
		}
		lockDigests[name] = prerendercontract.HexDigest(lockBytes)
	}
	packageDigest, err := prerendercontract.MergeInputDigests(packageDigests)
	if err != nil {
		return "", "", websiteapi.ErrUnavailable
	}
	lockDigest, err := prerendercontract.MergeInputDigests(lockDigests)
	if err != nil {
		return "", "", websiteapi.ErrUnavailable
	}
	return packageDigest, lockDigest, nil
}

func sortedValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// RenderPagePlan converts only trusted, already-validated Website inputs into
// the bounded document submitted to the isolated renderer.
func RenderPagePlan(input prerendercontract.TrustedInput, config json.RawMessage) (prerendercontract.PagePlan, error) {
	if err := prerendercontract.ValidateTrustedInput(input); err != nil || len(config) == 0 || len(config) > 1<<20 {
		return prerendercontract.PagePlan{}, websiteapi.ErrUnavailable
	}
	marker := input.Locale + ":" + input.SnapshotDigest[:12]
	escapedLocale := html.EscapeString(input.Locale)
	body := "<!doctype html><html lang=\"" + escapedLocale + "\"><head><meta charset=\"utf-8\"><title>" +
		escapedLocale + "</title></head><body><main id=\"app\" data-build=\"" + html.EscapeString(marker) +
		"\"><pre data-config>" + html.EscapeString(string(config)) + "</pre></main>" +
		"<script>document.documentElement.setAttribute('data-rendered','" + escapedLocale + "');</script></body></html>"
	return prerendercontract.PagePlan{Title: input.Locale, BodyHTML: body}, nil
}

// AssertModuleBuildInputResolver keeps the core-only capability dependency
// explicit for composition and compile-time checks.
var (
	_ BuildInputResolver       = (*ModuleBuildInputResolver)(nil)
	_ PinnedBuildInputResolver = (*ModuleBuildInputResolver)(nil)
)

var errBuildInputUnavailable = errors.New("trusted build input unavailable")

func describeBuildInputError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", errBuildInputUnavailable, err)
}

var _ = recorddata.ErrRecordNotFound
