package render

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ErrInvalidView is the sentinel for every rejected PageView. Callers can use
// errors.Is to distinguish a bad view from template or writer failures.
var ErrInvalidView = errors.New("invalid page view")

const (
	// BootstrapElementID is the inert script element carrying the JSON
	// bootstrap. It is the only script element without a content-addressed src.
	BootstrapElementID = "website-bootstrap"
	// MainElementID is the single page-level main landmark.
	MainElementID = "website-main"
)

var (
	//go:embed page.tmpl
	pageTemplateSource string

	pageTemplate = template.Must(template.New("page").Parse(pageTemplateSource))

	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	slotPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	integrityPattern  = regexp.MustCompile(`^(sha256|sha384|sha512)-[A-Za-z0-9+/=_-]{16,}$`)
)

// PlacementRegion selects where a placement root is emitted. The empty value
// means PlacementRegionMain, so an omitted region never silently moves primary
// content outside the single main landmark.
type PlacementRegion string

const (
	PlacementRegionMain    PlacementRegion = "main"
	PlacementRegionOutside PlacementRegion = "outside"
)

// LifecycleOperation is an initial static-lifecycle phase. The empty value
// means LifecycleRender.
type LifecycleOperation string

const (
	LifecycleRender  LifecycleOperation = "render"
	LifecycleHydrate LifecycleOperation = "hydrate"
)

// LifecycleAttrs carries the serializable attributes used by the browser
// runtime to attach to one already-rendered placement root.
type LifecycleAttrs struct {
	Operation  LifecycleOperation
	FeatureKey string
	SignalID   string
}

// AssetKind selects the emitted element for a content-addressed runtime asset.
type AssetKind string

const (
	AssetStyle  AssetKind = "style"
	AssetScript AssetKind = "script"
)

// Asset is a content-addressed runtime file with its Subresource Integrity
// metadata. Inline code is never accepted; use RuntimeAssets instead.
type Asset struct {
	Kind      AssetKind
	Path      string
	Integrity string
}

// PlacementView is one ordered, independent lifecycle root. HTML must be a
// ReadyHTML value produced by NewReadyHTML from trusted output; a plain string
// cannot be assigned here.
type PlacementView struct {
	ID        string
	Slot      string
	Region    PlacementRegion
	HTML      ReadyHTML
	Lifecycle LifecycleAttrs
}

// PageView is the complete, already-resolved input of the document renderer.
// It contains no executable inputs: CSS and JS are accepted only as empty
// placeholders and always cause a validation error when set.
type PageView struct {
	Title         string
	Description   string
	CanonicalURL  string
	Robots        string
	OGMetadata    map[string]string
	StatusCode    int
	Placements    []PlacementView
	Bootstrap     any
	RuntimeAssets []Asset
	RenderedAt    time.Time
	Identity      string

	// CSS and JS are reserved but unsupported fields. Keeping them in the view
	// makes an accidental inline-code handoff explicit and fail-closed.
	CSS string
	JS  string
}

// bootstrapJSON is a private json.Marshaler. html/template emits a
// json.Marshaler as raw JSON in a script context without quoting it as a
// JavaScript string, while still applying its JSON-safe escaping. It avoids
// exposing template.JS for visitor-derived data.
type bootstrapJSON struct {
	data []byte
}

func (b bootstrapJSON) MarshalJSON() ([]byte, error) {
	return append([]byte(nil), b.data...), nil
}

type placementData struct {
	ID         string
	Slot       string
	Operation  string
	FeatureKey string
	SignalID   string
	HTML       template.HTML
}

type assetData struct {
	Path      string
	Integrity string
}

type documentData struct {
	Title             string
	Description       string
	CanonicalURL      string
	Robots            string
	OGMetadata        map[string]string
	MainPlacements    []placementData
	OutsidePlacements []placementData
	Stylesheets       []assetData
	Scripts           []assetData
	BootstrapJSON     bootstrapJSON
	BootstrapID       string
	MainID            string
	Identity          string
	RenderedAt        string
}

// RenderPage returns the complete document. It returns no bytes for an invalid
// view, so a rejected result can never be cached or written as a response.
func RenderPage(view PageView) ([]byte, error) {
	var buf bytes.Buffer
	if err := RenderPageTo(&buf, view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderPageTo writes the complete document to w. Validation happens before
// any write; the final document is buffered so a writer failure cannot expose
// a partial page.
func RenderPageTo(w io.Writer, view PageView) error {
	data, err := prepareView(view)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, data); err != nil {
		return fmt.Errorf("render page: %w", err)
	}
	if _, err := buf.WriteTo(w); err != nil {
		return fmt.Errorf("write page: %w", err)
	}
	return nil
}

func prepareView(view PageView) (documentData, error) {
	if err := validateEnvelope(view); err != nil {
		return documentData{}, err
	}

	data := documentData{
		Title:        view.Title,
		Description:  view.Description,
		CanonicalURL: view.CanonicalURL,
		Robots:       view.Robots,
		OGMetadata:   view.OGMetadata,
		BootstrapID:  BootstrapElementID,
		MainID:       MainElementID,
		Identity:     view.Identity,
	}
	if !view.RenderedAt.IsZero() {
		data.RenderedAt = view.RenderedAt.UTC().Format(time.RFC3339)
	}

	seen := make(map[string]struct{}, len(view.Placements))
	for i, placement := range view.Placements {
		prepared, region, err := preparePlacement(placement)
		if err != nil {
			return documentData{}, fmt.Errorf("%w: placement %d: %v", ErrInvalidView, i, err)
		}
		if _, duplicate := seen[placement.ID]; duplicate {
			return documentData{}, fmt.Errorf("%w: duplicate placement ID %q", ErrInvalidView, placement.ID)
		}
		seen[placement.ID] = struct{}{}
		switch region {
		case PlacementRegionMain:
			data.MainPlacements = append(data.MainPlacements, prepared)
		case PlacementRegionOutside:
			data.OutsidePlacements = append(data.OutsidePlacements, prepared)
		}
	}

	for i, asset := range view.RuntimeAssets {
		prepared, err := prepareAsset(asset)
		if err != nil {
			return documentData{}, fmt.Errorf("%w: runtime asset %d: %v", ErrInvalidView, i, err)
		}
		switch asset.Kind {
		case AssetStyle:
			data.Stylesheets = append(data.Stylesheets, prepared)
		case AssetScript:
			data.Scripts = append(data.Scripts, prepared)
		}
	}

	bootstrap, err := encodeBootstrap(view.Bootstrap)
	if err != nil {
		return documentData{}, err
	}
	data.BootstrapJSON = bootstrap
	return data, nil
}

func validateEnvelope(view PageView) error {
	switch view.StatusCode {
	case 200, 404, 500, 503:
	default:
		return fmt.Errorf("%w: unsupported status code %d", ErrInvalidView, view.StatusCode)
	}
	if strings.TrimSpace(view.Title) == "" {
		return fmt.Errorf("%w: title is required", ErrInvalidView)
	}
	if strings.TrimSpace(view.CSS) != "" || strings.TrimSpace(view.JS) != "" {
		return fmt.Errorf("%w: inline CSS and JS are not accepted", ErrInvalidView)
	}
	if view.CanonicalURL != "" {
		if err := validateCanonical(view.CanonicalURL); err != nil {
			return err
		}
	} else if view.StatusCode == 200 {
		return fmt.Errorf("%w: canonical URL is required for status 200", ErrInvalidView)
	}
	if view.StatusCode != 200 && !strings.Contains(strings.ToLower(view.Robots), "noindex") {
		return fmt.Errorf("%w: error statuses require noindex robots", ErrInvalidView)
	}
	if view.StatusCode == 200 {
		if strings.TrimSpace(view.Identity) == "" {
			return fmt.Errorf("%w: document identity is required for status 200", ErrInvalidView)
		}
		if view.RenderedAt.IsZero() {
			return fmt.Errorf("%w: render time is required for status 200", ErrInvalidView)
		}
	}
	return nil
}

func preparePlacement(placement PlacementView) (placementData, PlacementRegion, error) {
	if !identifierPattern.MatchString(placement.ID) {
		return placementData{}, "", fmt.Errorf("invalid placement ID %q", placement.ID)
	}
	if !slotPattern.MatchString(placement.Slot) {
		return placementData{}, "", fmt.Errorf("invalid slot %q", placement.Slot)
	}
	if placement.HTML.isEmpty() {
		return placementData{}, "", errors.New("missing ready HTML")
	}

	region := placement.Region
	if region == "" {
		region = PlacementRegionMain
	}
	if region != PlacementRegionMain && region != PlacementRegionOutside {
		return placementData{}, "", fmt.Errorf("invalid region %q", region)
	}

	operation := placement.Lifecycle.Operation
	if operation == "" {
		operation = LifecycleRender
	}
	if operation != LifecycleRender && operation != LifecycleHydrate {
		return placementData{}, "", fmt.Errorf("invalid lifecycle operation %q", operation)
	}
	if key := placement.Lifecycle.FeatureKey; key != "" && !identifierPattern.MatchString(key) {
		return placementData{}, "", fmt.Errorf("invalid feature key %q", key)
	}
	if signal := placement.Lifecycle.SignalID; signal != "" && !identifierPattern.MatchString(signal) {
		return placementData{}, "", fmt.Errorf("invalid signal ID %q", signal)
	}

	// ReadyHTML is the only value promoted to a trusted HTML type in this
	// package; it has already passed NewReadyHTML's structural validation.
	prepared := placementData{
		ID:         placement.ID,
		Slot:       placement.Slot,
		Operation:  string(operation),
		FeatureKey: placement.Lifecycle.FeatureKey,
		SignalID:   placement.Lifecycle.SignalID,
		HTML:       template.HTML(placement.HTML.bytes),
	}
	return prepared, region, nil
}

func prepareAsset(asset Asset) (assetData, error) {
	switch asset.Kind {
	case AssetStyle, AssetScript:
	default:
		return assetData{}, fmt.Errorf("invalid asset kind %q", asset.Kind)
	}
	if !validAssetPath(asset.Path) {
		return assetData{}, fmt.Errorf("invalid asset path %q", asset.Path)
	}
	if !integrityPattern.MatchString(asset.Integrity) {
		return assetData{}, fmt.Errorf("invalid integrity metadata")
	}
	return assetData{Path: asset.Path, Integrity: asset.Integrity}, nil
}

func validAssetPath(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, ".") ||
		strings.Contains(value, "..") || strings.Contains(value, "//") ||
		strings.ContainsAny(value, ":\\\r\n\t ") {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validateCanonical(raw string) error {
	if strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t") {
		return fmt.Errorf("%w: invalid canonical URL", ErrInvalidView)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid canonical URL: %v", ErrInvalidView, err)
	}
	if !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" ||
		parsed.Opaque != "" {
		return fmt.Errorf("%w: invalid canonical URL", ErrInvalidView)
	}
	return nil
}

func encodeBootstrap(value any) (bootstrapJSON, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		return bootstrapJSON{}, fmt.Errorf("%w: bootstrap is not JSON-serializable: %v", ErrInvalidView, err)
	}
	data := bytes.TrimSpace(buf.Bytes())
	if len(data) == 0 || data[0] != '{' {
		return bootstrapJSON{}, fmt.Errorf("%w: bootstrap must be a JSON object", ErrInvalidView)
	}
	return bootstrapJSON{data: data}, nil
}
