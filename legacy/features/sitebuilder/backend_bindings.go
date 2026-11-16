package sitebuilder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/textproto"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PublicBackend is a named, public origin registered inside one snapshot.
// It contains no credentials and is never contacted by the engine server.
type PublicBackend struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
}

// PublicSnapshotConstant is a typed, public value a feature request may use.
// Objects and nulls are deliberately excluded from this small contract.
type PublicSnapshotConstant struct {
	Key   string          `json:"key"`
	Label string          `json:"label"`
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

// FeatureRequestSlot declares how one feature request is configured. Argument
// names point to object properties in the existing manifest arguments schema,
// preserving the FeatureContext ABI.
type FeatureRequestSlot struct {
	Key          string                      `json:"key"`
	Label        string                      `json:"label"`
	Argument     string                      `json:"argument"`
	PathLabel    string                      `json:"pathLabel,omitempty"`
	DefaultPath  string                      `json:"defaultPath"`
	Destinations []FeatureRequestDestination `json:"destinations"`
}

// FeatureRequestDestination is an explicit, manifest-authored target for a
// request value. Sources and destinations are allowlisted by the manifest.
type FeatureRequestDestination struct {
	Kind      string   `json:"kind"`
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Type      string   `json:"type"`
	ItemsType string   `json:"itemsType,omitempty"`
	Sources   []string `json:"sources"`
	Required  bool     `json:"required,omitempty"`
}

// BackendRequestBinding configures one request slot on one placement.
// BackendID is empty for a relative request to the page's current origin.
type BackendRequestBinding struct {
	BackendID string                `json:"backendId,omitempty"`
	Path      string                `json:"path"`
	Bindings  []BackendValueBinding `json:"bindings,omitempty"`
}

type BackendValueBinding struct {
	Destination BackendValueDestination `json:"destination"`
	Source      BackendValueSource      `json:"source"`
}

type BackendValueDestination struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

type BackendValueSource struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

// ResolvedBackendRequest is passed to a feature as its manifest-declared
// request argument. HTTP execution, serialization, and response handling stay
// in feature code.
type ResolvedBackendRequest struct {
	BaseURL string                       `json:"baseUrl,omitempty"`
	Path    string                       `json:"path"`
	Query   map[string][]json.RawMessage `json:"query,omitempty"`
	Body    map[string]json.RawMessage   `json:"body,omitempty"`
	Headers map[string]string            `json:"headers,omitempty"`
	Error   string                       `json:"error,omitempty"`
}

var (
	publicConfigKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	validRequestValueTypes = map[string]bool{"string": true, "integer": true, "number": true, "boolean": true}
)

var forbiddenPublicValueNames = []string{"authorization", "cookie", "credential", "password", "secret", "token", "apikey", "privatekey"}

func validatePublicBackendRegistry(backends []PublicBackend, constants []PublicSnapshotConstant) error {
	if len(backends) > 100 {
		return errors.New("at most 100 public backends may be registered in a snapshot")
	}
	ids := make(map[string]bool, len(backends))
	names := make(map[string]bool, len(backends))
	for _, backend := range backends {
		id, err := parseBackendID(backend.ID)
		if err != nil {
			return fmt.Errorf("public backend ID is invalid: %w", err)
		}
		if ids[id] {
			return fmt.Errorf("duplicate public backend ID %q", id)
		}
		ids[id] = true
		name := strings.TrimSpace(backend.Name)
		if name == "" || len([]rune(name)) > 100 {
			return errors.New("public backend name must contain 1–100 characters")
		}
		folded := strings.ToLower(name)
		if names[folded] {
			return fmt.Errorf("duplicate public backend name %q", name)
		}
		names[folded] = true
		if _, err := ValidatePublicBackendBaseURL(backend.BaseURL); err != nil {
			return fmt.Errorf("public backend %q base URL: %w", name, err)
		}
	}
	if len(constants) > 200 {
		return errors.New("at most 200 public constants may be registered in a snapshot")
	}
	constantKeys := make(map[string]bool, len(constants))
	for _, constant := range constants {
		if !publicConfigKeyPattern.MatchString(constant.Key) || isSensitiveConfigName(constant.Key) {
			return fmt.Errorf("public constant key %q is invalid or sensitive", constant.Key)
		}
		if constantKeys[constant.Key] {
			return fmt.Errorf("duplicate public constant key %q", constant.Key)
		}
		constantKeys[constant.Key] = true
		if strings.TrimSpace(constant.Label) == "" || len([]rune(constant.Label)) > 100 {
			return fmt.Errorf("public constant %q label must contain 1–100 characters", constant.Key)
		}
		if err := validatePublicConstantValue(constant.Type, constant.Value); err != nil {
			return fmt.Errorf("public constant %q: %w", constant.Key, err)
		}
	}
	return nil
}

func parseBackendID(raw string) (string, error) {
	parsed, err := uuidParse(raw)
	if err != nil || parsed == "00000000-0000-0000-0000-000000000000" {
		return "", errors.New("ID must be a generated non-zero UUID")
	}
	return parsed, nil
}

// uuidParse keeps the UUID dependency local to this package without allowing
// alternate textual spellings in snapshot payloads.
func uuidParse(raw string) (string, error) {
	// uuid.Parse accepts canonical dashed UUIDs and several compatibility
	// spellings; the serialized contract uses canonical lower-case text only.
	if len(raw) != 36 || strings.ToLower(raw) != raw || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' {
		return "", errors.New("expected canonical lowercase UUID")
	}
	for index, char := range raw {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return "", errors.New("expected canonical lowercase UUID")
		}
	}
	return raw, nil
}

// ValidatePublicBackendBaseURL only accepts a public HTTP(S) origin. The
// engine never resolves or contacts this URL; browser calls are governed by
// browser CORS, while prerender never receives external-origin access.
func ValidatePublicBackendBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	parsed, err := url.Parse(value)
	if err != nil || value == "" || parsed == nil || !parsed.IsAbs() || parsed.Opaque != "" {
		return "", errors.New("must be an absolute HTTP(S) URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("scheme must be http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("credentials, query and fragment are not allowed")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("only an origin is allowed; put the endpoint path in the placement")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || strings.Contains(host, "%") || strings.HasSuffix(host, ".") {
		return "", errors.New("host name is invalid")
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", errors.New("port must be between 1 and 65535")
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicUnicastIP(ip) {
			return "", errors.New("host must be a public unicast address")
		}
	} else if err := validatePublicDNSName(host); err != nil {
		return "", err
	}
	return scheme + "://" + strings.ToLower(parsed.Host), nil
}

func validatePublicDNSName(host string) error {
	for _, suffix := range []string{".localhost", ".local", ".internal", ".home.arpa", ".lan", ".test", ".invalid", ".example"} {
		if host == strings.TrimPrefix(suffix, ".") || strings.HasSuffix(host, suffix) {
			return errors.New("host must be a public DNS name")
		}
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 || len(host) > 253 {
		return errors.New("host must be a fully qualified public DNS name")
	}
	allNumeric := true
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("DNS labels are invalid")
		}
		for _, char := range label {
			if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
				return errors.New("host must use an ASCII DNS name; use its punycode form when needed")
			}
		}
		if _, err := strconv.ParseUint(label, 0, 64); err != nil {
			allNumeric = false
		}
	}
	if allNumeric || numericLikeLabel(labels[len(labels)-1]) {
		return errors.New("numeric host names are not allowed")
	}
	return nil
}

func numericLikeLabel(label string) bool {
	if label != "" {
		allDigits := true
		for _, char := range label {
			if char < '0' || char > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return true
		}
	}
	if _, err := strconv.ParseUint(label, 0, 64); err == nil {
		return true
	}
	return false
}

func isPublicUnicastIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		for _, network := range []string{
			"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		} {
			_, subnet, _ := net.ParseCIDR(network)
			if subnet.Contains(v4) {
				return false
			}
		}
		return true
	}
	for _, network := range []string{"2001:db8::/32", "64:ff9b:1::/48"} {
		_, subnet, _ := net.ParseCIDR(network)
		if subnet.Contains(ip) {
			return false
		}
	}
	return true
}

func validatePublicConstantValue(kind string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("value is required")
	}
	value, err := decodeJSONValue(raw)
	if err != nil {
		return errors.New("value is invalid JSON")
	}
	if err := validateValueForType(value, kind, ""); err != nil {
		return err
	}
	return nil
}

func isSensitiveConfigName(value string) bool {
	canonical := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(value))
	for _, forbidden := range forbiddenPublicValueNames {
		if strings.Contains(canonical, forbidden) {
			return true
		}
	}
	return false
}

func validatePlacementBackendRequestShape(config SiteConfig, placement Placement) error {
	backendIDs := make(map[string]bool, len(config.Backends))
	for _, backend := range config.Backends {
		id, err := parseBackendID(backend.ID)
		if err == nil {
			backendIDs[id] = true
		}
	}
	for key, request := range placement.BackendRequests {
		if !publicConfigKeyPattern.MatchString(key) {
			return fmt.Errorf("placement %q has an invalid backend request key %q", placement.ID, key)
		}
		if request.BackendID != "" {
			id, err := parseBackendID(request.BackendID)
			if err != nil || !backendIDs[id] {
				return fmt.Errorf("placement %q references an unknown public backend", placement.ID)
			}
		}
		if err := validateRequestPathTemplate(request.Path); err != nil {
			return fmt.Errorf("placement %q request %q path: %w", placement.ID, key, err)
		}
		seen := make(map[string]bool, len(request.Bindings))
		for _, binding := range request.Bindings {
			if !publicConfigKeyPattern.MatchString(binding.Source.Key) {
				return fmt.Errorf("placement %q request %q has an invalid source key", placement.ID, key)
			}
			if binding.Source.Kind != "route" && binding.Source.Kind != "query" && binding.Source.Kind != "argument" && binding.Source.Kind != "constant" {
				return fmt.Errorf("placement %q request %q has an unsupported source kind %q", placement.ID, key, binding.Source.Kind)
			}
			if binding.Destination.Kind != "path" && binding.Destination.Kind != "query" && binding.Destination.Kind != "body" && binding.Destination.Kind != "header" {
				return fmt.Errorf("placement %q request %q has an unsupported destination kind %q", placement.ID, key, binding.Destination.Kind)
			}
			if !publicConfigKeyPattern.MatchString(binding.Destination.Key) && binding.Destination.Kind != "header" {
				return fmt.Errorf("placement %q request %q has an invalid destination key", placement.ID, key)
			}
			if binding.Destination.Kind == "header" {
				if !validPublicHeaderName(binding.Destination.Key) || isSensitiveConfigName(binding.Destination.Key) {
					return fmt.Errorf("placement %q request %q uses a forbidden or invalid header", placement.ID, key)
				}
			}
			identity := binding.Destination.Kind + "\x00" + strings.ToLower(binding.Destination.Key)
			if seen[identity] {
				return fmt.Errorf("placement %q request %q has duplicate destination %s", placement.ID, key, binding.Destination.Key)
			}
			seen[identity] = true
		}
	}
	return nil
}

func validPublicHeaderName(name string) bool {
	if name == "" || textproto.CanonicalMIMEHeaderKey(name) == "" || isForbiddenPublicHeader(name) {
		return false
	}
	for _, char := range name {
		if !isHTTPTokenByte(byte(char)) {
			return false
		}
	}
	return true
}

func isForbiddenPublicHeader(name string) bool {
	canonical := strings.ToLower(name)
	if isSensitiveConfigName(canonical) {
		return true
	}
	switch canonical {
	case "host", "origin", "connection", "content-length", "transfer-encoding", "te", "trailer", "upgrade", "proxy-connection", "referer", "accept-encoding":
		return true
	}
	return strings.HasPrefix(canonical, "proxy-") || strings.HasPrefix(canonical, "sec-")
}

func isHTTPTokenByte(char byte) bool {
	if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
		return true
	}
	return strings.ContainsRune("!#$%&'*+-.^_`|~", rune(char))
}

func validateFeatureRequestSlots(arguments json.RawMessage, requests []FeatureRequestSlot) error {
	if len(requests) > 32 {
		return errors.New("manifest may declare at most 32 request slots")
	}
	var schema struct {
		Properties map[string]struct {
			Type                string          `json:"type"`
			Default             json.RawMessage `json:"default"`
			EditorManagedBySite bool            `json:"x-editor-managed-by-site"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(arguments, &schema); err != nil {
		return fmt.Errorf("decode manifest arguments: %w", err)
	}
	seenKeys, seenArguments := map[string]bool{}, map[string]bool{}
	for _, request := range requests {
		if !publicConfigKeyPattern.MatchString(request.Key) {
			return fmt.Errorf("request slot key %q is invalid", request.Key)
		}
		if seenKeys[request.Key] {
			return fmt.Errorf("duplicate request slot %q", request.Key)
		}
		seenKeys[request.Key] = true
		if strings.TrimSpace(request.Label) == "" || len([]rune(request.Label)) > 100 {
			return fmt.Errorf("request slot %q label must contain 1–100 characters", request.Key)
		}
		if !publicConfigKeyPattern.MatchString(request.Argument) || seenArguments[request.Argument] {
			return fmt.Errorf("request slot %q has an invalid or duplicate managed argument", request.Key)
		}
		seenArguments[request.Argument] = true
		argument, ok := schema.Properties[request.Argument]
		if !ok || argument.Type != "object" || !argument.EditorManagedBySite || len(argument.Default) > 0 || contains(schema.Required, request.Argument) {
			return fmt.Errorf("request slot %q must reference an optional object argument marked x-editor-managed-by-site without a default", request.Key)
		}
		if err := validateRequestPathTemplate(request.DefaultPath); err != nil {
			return fmt.Errorf("request slot %q defaultPath: %w", request.Key, err)
		}
		if len(request.Destinations) == 0 {
			return fmt.Errorf("request slot %q must declare at least one destination", request.Key)
		}
		seenDestinations := map[string]bool{}
		allowedSources := map[string]bool{"route": true, "query": true, "argument": true, "constant": true}
		pathDestinations := map[string]bool{}
		for _, destination := range request.Destinations {
			if destination.Kind != "path" && destination.Kind != "query" && destination.Kind != "body" && destination.Kind != "header" {
				return fmt.Errorf("request slot %q has unsupported destination kind %q", request.Key, destination.Kind)
			}
			if destination.Kind == "header" {
				if !validPublicHeaderName(destination.Key) || isSensitiveConfigName(destination.Key) {
					return fmt.Errorf("request slot %q has a forbidden or invalid header name", request.Key)
				}
			} else if !publicConfigKeyPattern.MatchString(destination.Key) {
				return fmt.Errorf("request slot %q has an invalid destination key %q", request.Key, destination.Key)
			}
			if strings.TrimSpace(destination.Label) == "" || len([]rune(destination.Label)) > 100 {
				return fmt.Errorf("request slot %q destination %q label is invalid", request.Key, destination.Key)
			}
			if !validRequestValueTypes[destination.Type] {
				if destination.Type != "array" || !validRequestValueTypes[destination.ItemsType] {
					return fmt.Errorf("request slot %q destination %q has an unsupported value type", request.Key, destination.Key)
				}
			} else if destination.ItemsType != "" {
				return fmt.Errorf("request slot %q destination %q may set itemsType only for arrays", request.Key, destination.Key)
			}
			if destination.Kind == "path" && destination.Type != "string" {
				return fmt.Errorf("request slot %q path destination %q must be a string", request.Key, destination.Key)
			}
			if destination.Kind == "header" && destination.Type != "string" {
				return fmt.Errorf("request slot %q header destination %q must be a string", request.Key, destination.Key)
			}
			identity := destination.Kind + "\x00" + strings.ToLower(destination.Key)
			if seenDestinations[identity] {
				return fmt.Errorf("request slot %q repeats destination %q", request.Key, destination.Key)
			}
			seenDestinations[identity] = true
			if destination.Kind == "path" {
				pathDestinations[destination.Key] = true
			}
			if len(destination.Sources) == 0 {
				return fmt.Errorf("request slot %q destination %q must allow at least one source", request.Key, destination.Key)
			}
			seenSourceKinds := map[string]bool{}
			for _, source := range destination.Sources {
				if !allowedSources[source] || seenSourceKinds[source] {
					return fmt.Errorf("request slot %q destination %q has unsupported or duplicate source %q", request.Key, destination.Key, source)
				}
				seenSourceKinds[source] = true
			}
		}
		placeholders, err := pathTemplatePlaceholders(request.DefaultPath)
		if err != nil {
			return fmt.Errorf("request slot %q defaultPath: %w", request.Key, err)
		}
		for _, placeholder := range placeholders {
			if !pathDestinations[placeholder] {
				return fmt.Errorf("request slot %q defaultPath uses undeclared path destination %q", request.Key, placeholder)
			}
		}
		for destination := range pathDestinations {
			if !contains(placeholders, destination) {
				return fmt.Errorf("request slot %q path destination %q is absent from defaultPath", request.Key, destination)
			}
		}
	}
	return nil
}

func validateRequestPathTemplate(value string) error {
	if value == "" || len(value) > 2048 || value[0] != '/' || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "?#\\%") {
		return errors.New("must be a relative absolute-path value beginning with one slash and without query, fragment, backslash or percent encoding")
	}
	segments := strings.Split(value[1:], "/")
	for index, segment := range segments {
		if segment == "" && index != len(segments)-1 {
			return errors.New("path must not contain empty segments")
		}
		if segment == "." || segment == ".." {
			return errors.New("path must not contain dot segments")
		}
		if strings.ContainsAny(segment, "{}") {
			if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' || !publicConfigKeyPattern.MatchString(segment[1:len(segment)-1]) {
				return errors.New("each path placeholder must occupy one whole segment and use a simple name")
			}
			continue
		}
		for _, char := range segment {
			if char <= 0x20 || char == 0x7f || char == '?' || char == '#' || char == '\\' {
				return errors.New("path contains an unsafe character")
			}
		}
	}
	probe := value
	for _, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			probe = strings.Replace(probe, segment, "probe", 1)
		}
	}
	parsed, err := url.ParseRequestURI(probe)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("path must be a valid relative HTTP path")
	}
	return nil
}

func pathTemplatePlaceholders(value string) ([]string, error) {
	if err := validateRequestPathTemplate(value); err != nil {
		return nil, err
	}
	segments := strings.Split(value[1:], "/")
	seen := map[string]bool{}
	var result []string
	for _, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			key := segment[1 : len(segment)-1]
			if seen[key] {
				return nil, fmt.Errorf("path placeholder %q is repeated", key)
			}
			seen[key] = true
			result = append(result, key)
		}
	}
	return result, nil
}

func validateBackendRequestContract(cfg SiteConfig, placement Placement, manifest Manifest, pageCaptures []string) error {
	contracts := make(map[string]FeatureRequestSlot, len(manifest.Requests))
	for _, slot := range manifest.Requests {
		contracts[slot.Key] = slot
	}
	argumentProperties := schemaArgumentProperties(manifest.Arguments)
	constantMap := make(map[string]PublicSnapshotConstant, len(cfg.PublicConstants))
	for _, constant := range cfg.PublicConstants {
		constantMap[constant.Key] = constant
	}
	pageParams := make(map[string]bool, len(pageCaptures))
	for _, capture := range pageCaptures {
		pageParams[capture] = true
	}
	for key, binding := range placement.BackendRequests {
		slot, ok := contracts[key]
		if !ok {
			return fmt.Errorf("placement %q configures undeclared request slot %q", placement.ID, key)
		}
		if err := validateRequestBindingForSlot(binding, slot, argumentProperties, constantMap, pageParams); err != nil {
			return fmt.Errorf("placement %q request %q: %w", placement.ID, key, err)
		}
	}
	for _, slot := range manifest.Requests {
		for _, destination := range slot.Destinations {
			binding, configured := placement.BackendRequests[slot.Key]
			if !destination.Required || !configured {
				continue
			}
			if !hasDestinationBinding(binding.Bindings, destination.Kind, destination.Key) {
				return fmt.Errorf("placement %q request %q requires destination %q to be bound", placement.ID, slot.Key, destination.Key)
			}
		}
	}
	for argument := range argumentProperties {
		if schemaPropertyEditorManagedSite(manifest.Arguments, argument) {
			if _, configured := placement.Arguments[argument]; configured {
				return fmt.Errorf("placement %q cannot manually set site-managed request argument %q", placement.ID, argument)
			}
		}
	}
	return nil
}

type argumentSchemaProperty struct {
	Type  string `json:"type"`
	Items struct {
		Type string `json:"type"`
	} `json:"items"`
	Managed bool `json:"x-editor-managed-by-site"`
}

func schemaArgumentProperties(schema json.RawMessage) map[string]argumentSchemaProperty {
	var object struct {
		Properties map[string]argumentSchemaProperty `json:"properties"`
	}
	_ = json.Unmarshal(schema, &object)
	if object.Properties == nil {
		object.Properties = map[string]argumentSchemaProperty{}
	}
	return object.Properties
}

func schemaPropertyEditorManagedSite(schema json.RawMessage, field string) bool {
	return schemaArgumentProperties(schema)[field].Managed
}

func validateRequestBindingForSlot(binding BackendRequestBinding, slot FeatureRequestSlot, argumentProperties map[string]argumentSchemaProperty, constants map[string]PublicSnapshotConstant, routeParams map[string]bool) error {
	path := binding.Path
	if path == "" {
		path = slot.DefaultPath
	}
	placeholders, err := pathTemplatePlaceholders(path)
	if err != nil {
		return fmt.Errorf("path: %w", err)
	}
	manifestDestinations := make(map[string]FeatureRequestDestination, len(slot.Destinations))
	for _, destination := range slot.Destinations {
		manifestDestinations[destination.Kind+"\x00"+destination.Key] = destination
	}
	seen := make(map[string]bool, len(binding.Bindings))
	boundPath := make(map[string]bool)
	for _, valueBinding := range binding.Bindings {
		identity := valueBinding.Destination.Kind + "\x00" + valueBinding.Destination.Key
		if seen[identity] {
			return fmt.Errorf("destination %q is bound more than once", valueBinding.Destination.Key)
		}
		seen[identity] = true
		destination, ok := manifestDestinations[identity]
		if !ok {
			return fmt.Errorf("destination %q is not declared by the manifest", valueBinding.Destination.Key)
		}
		if !contains(destination.Sources, valueBinding.Source.Kind) {
			return fmt.Errorf("source kind %q is not allowed for destination %q", valueBinding.Source.Kind, valueBinding.Destination.Key)
		}
		if !publicConfigKeyPattern.MatchString(valueBinding.Source.Key) {
			return fmt.Errorf("source key %q is invalid", valueBinding.Source.Key)
		}
		switch valueBinding.Source.Kind {
		case "route":
			if !routeParams[valueBinding.Source.Key] {
				return fmt.Errorf("route parameter %q is not available on this page", valueBinding.Source.Key)
			}
		case "query":
		case "argument":
			property, exists := argumentProperties[valueBinding.Source.Key]
			if !exists || property.Managed || property.Type == "object" || property.Type == "null" {
				return fmt.Errorf("feature argument %q is not an available scalar source", valueBinding.Source.Key)
			}
			if !argumentTypeCanPopulate(property, destination) {
				return fmt.Errorf("feature argument %q is incompatible with destination %q", valueBinding.Source.Key, destination.Key)
			}
		case "constant":
			constant, exists := constants[valueBinding.Source.Key]
			if !exists {
				return fmt.Errorf("public constant %q is not configured", valueBinding.Source.Key)
			}
			if !constantTypeCanPopulate(constant.Type, destination) {
				return fmt.Errorf("public constant %q is incompatible with destination %q", valueBinding.Source.Key, destination.Key)
			}
		default:
			return fmt.Errorf("source kind %q is unsupported", valueBinding.Source.Kind)
		}
		if valueBinding.Source.Kind == "route" || valueBinding.Source.Kind == "query" {
			if !stringSourceCanPopulate(destination) {
				return fmt.Errorf("%s source cannot populate destination %q", valueBinding.Source.Kind, destination.Key)
			}
		}
		if destination.Kind == "path" {
			boundPath[destination.Key] = true
		}
	}
	for _, placeholder := range placeholders {
		if !boundPath[placeholder] {
			return fmt.Errorf("path parameter %q has no source binding", placeholder)
		}
	}
	placeholderSet := make(map[string]bool, len(placeholders))
	for _, placeholder := range placeholders {
		placeholderSet[placeholder] = true
	}
	for _, valueBinding := range binding.Bindings {
		if valueBinding.Destination.Kind == "path" && !placeholderSet[valueBinding.Destination.Key] {
			return fmt.Errorf("path destination %q is not present in the selected path", valueBinding.Destination.Key)
		}
	}
	for _, destination := range slot.Destinations {
		if destination.Required && !seen[destination.Kind+"\x00"+destination.Key] {
			return fmt.Errorf("required destination %q has no source binding", destination.Key)
		}
	}
	return nil
}

func hasDestinationBinding(bindings []BackendValueBinding, kind, key string) bool {
	for _, binding := range bindings {
		if binding.Destination.Kind == kind && binding.Destination.Key == key {
			return true
		}
	}
	return false
}

func stringSourceCanPopulate(destination FeatureRequestDestination) bool {
	if destination.Type == "array" {
		return validRequestValueTypes[destination.ItemsType]
	}
	return validRequestValueTypes[destination.Type]
}

func argumentTypeCanPopulate(source argumentSchemaProperty, destination FeatureRequestDestination) bool {
	if destination.Type == "array" {
		if source.Type == "array" {
			return source.Items.Type == destination.ItemsType
		}
		return source.Type == destination.ItemsType
	}
	if source.Type == "array" {
		return false
	}
	if destination.Type == "number" {
		return source.Type == "number" || source.Type == "integer"
	}
	return source.Type == destination.Type
}

func constantTypeCanPopulate(source string, destination FeatureRequestDestination) bool {
	if destination.Type == "array" {
		if source == "string-array" {
			return destination.ItemsType == "string"
		}
		return source == destination.ItemsType
	}
	if source == "string-array" {
		return false
	}
	if destination.Type == "number" {
		return source == "number" || source == "integer"
	}
	return source == destination.Type
}

func resolvePlacementBackendRequests(manifest Manifest, config SiteConfig, placement Placement, arguments json.RawMessage, routeArgs map[string]string, incomingQuery url.Values) (json.RawMessage, bool, error) {
	if len(placement.BackendRequests) == 0 {
		return arguments, false, nil
	}
	var resolvedArguments map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &resolvedArguments); err != nil {
		return nil, false, fmt.Errorf("decode resolved arguments: %w", err)
	}
	if resolvedArguments == nil {
		resolvedArguments = map[string]json.RawMessage{}
	}
	backends := make(map[string]PublicBackend, len(config.Backends))
	for _, backend := range config.Backends {
		id, err := parseBackendID(backend.ID)
		if err == nil {
			backends[id] = backend
		}
	}
	constants := make(map[string]PublicSnapshotConstant, len(config.PublicConstants))
	for _, constant := range config.PublicConstants {
		constants[constant.Key] = constant
	}
	properties := schemaArgumentProperties(manifest.Arguments)
	external := false
	for _, slot := range manifest.Requests {
		binding, configured := placement.BackendRequests[slot.Key]
		if !configured {
			continue
		}
		settings := ResolvedBackendRequest{Path: slot.DefaultPath, Query: map[string][]json.RawMessage{}, Body: map[string]json.RawMessage{}, Headers: map[string]string{}}
		if binding.Path != "" {
			settings.Path = binding.Path
		}
		if binding.BackendID != "" {
			id, err := parseBackendID(binding.BackendID)
			backend, exists := backends[id]
			if err != nil || !exists {
				settings.Error = "unknown_backend"
			} else {
				var err error
				settings.BaseURL, err = ValidatePublicBackendBaseURL(backend.BaseURL)
				if err != nil {
					settings.Error = "invalid_backend"
				}
				external = true
			}
		}
		if settings.Error == "" {
			pathValues := make(map[string]json.RawMessage)
			for _, valueBinding := range binding.Bindings {
				destination, _ := requestDestinationForSlot(slot, valueBinding.Destination.Kind, valueBinding.Destination.Key)
				value, present, valueErr := resolveBackendSource(valueBinding.Source, destination, routeArgs, incomingQuery, resolvedArguments, properties, constants)
				if valueErr != nil {
					settings.Error = "invalid_source"
					break
				}
				if !present {
					settings.Error = "missing_source"
					break
				}
				switch destination.Kind {
				case "path":
					pathValues[destination.Key] = value
				case "query":
					values, err := asRequestRepeatedValues(value, destination)
					if err != nil {
						settings.Error = "invalid_source"
						break
					}
					settings.Query[destination.Key] = values
				case "body":
					settings.Body[destination.Key] = value
				case "header":
					var text string
					if json.Unmarshal(value, &text) != nil || strings.ContainsAny(text, "\r\n") {
						settings.Error = "invalid_source"
						break
					}
					settings.Headers[destination.Key] = text
				}
				if settings.Error != "" {
					break
				}
			}
			if settings.Error == "" {
				resolvedPath, err := resolveRequestPath(settings.Path, pathValues)
				if err != nil {
					settings.Error = "invalid_source"
				} else {
					settings.Path = resolvedPath
				}
			}
		}
		if settings.Error != "" {
			settings.Query = nil
			settings.Body = nil
			settings.Headers = nil
		}
		encoded, err := json.Marshal(settings)
		if err != nil {
			return nil, external, err
		}
		resolvedArguments[slot.Argument] = encoded
	}
	encoded, err := json.Marshal(resolvedArguments)
	return encoded, external, err
}

func publicBackendOriginsForPlacement(manifest Manifest, config SiteConfig, placement Placement) []string {
	byID := make(map[string]PublicBackend, len(config.Backends))
	for _, backend := range config.Backends {
		id, err := parseBackendID(backend.ID)
		if err == nil {
			byID[id] = backend
		}
	}
	origins := map[string]bool{}
	for _, slot := range manifest.Requests {
		binding, configured := placement.BackendRequests[slot.Key]
		if !configured {
			continue
		}
		if binding.BackendID == "" {
			continue
		}
		id, err := parseBackendID(binding.BackendID)
		if err != nil {
			continue
		}
		backend, ok := byID[id]
		if !ok {
			continue
		}
		origin, err := ValidatePublicBackendBaseURL(backend.BaseURL)
		if err == nil {
			origins[origin] = true
		}
	}
	result := make([]string, 0, len(origins))
	for origin := range origins {
		result = append(result, origin)
	}
	sort.Strings(result)
	return result
}

func requestDestinationForSlot(slot FeatureRequestSlot, kind, key string) (FeatureRequestDestination, bool) {
	for _, destination := range slot.Destinations {
		if destination.Kind == kind && destination.Key == key {
			return destination, true
		}
	}
	return FeatureRequestDestination{}, false
}

func resolveBackendSource(source BackendValueSource, destination FeatureRequestDestination, routeArgs map[string]string, query url.Values, arguments map[string]json.RawMessage, properties map[string]argumentSchemaProperty, constants map[string]PublicSnapshotConstant) (json.RawMessage, bool, error) {
	switch source.Kind {
	case "route":
		value, ok := routeArgs[source.Key]
		if !ok {
			return nil, false, nil
		}
		return convertStringSource(value, destination)
	case "query":
		values, ok := query[source.Key]
		if !ok {
			return nil, false, nil
		}
		if destination.Type == "array" {
			items := make([]json.RawMessage, 0, len(values))
			for _, value := range values {
				item, present, err := convertStringScalar(value, destination.ItemsType)
				if err != nil || !present {
					return nil, true, err
				}
				items = append(items, item)
			}
			encoded, err := json.Marshal(items)
			return encoded, true, err
		}
		if len(values) != 1 {
			return nil, true, errors.New("repeated query values require an array destination")
		}
		return convertStringScalar(values[0], destination.Type)
	case "argument":
		raw, ok := arguments[source.Key]
		if !ok {
			return nil, false, nil
		}
		value, err := decodeJSONValue(raw)
		if err != nil {
			return nil, true, err
		}
		if value == nil {
			return nil, true, errors.New("null is not a request source")
		}
		return convertRequestValue(value, properties[source.Key], destination)
	case "constant":
		constant, ok := constants[source.Key]
		if !ok {
			return nil, false, nil
		}
		value, err := decodeJSONValue(constant.Value)
		if err != nil {
			return nil, true, err
		}
		return convertRequestValue(value, argumentSchemaProperty{Type: constantTypeToSchema(constant.Type), Items: struct {
			Type string `json:"type"`
		}{Type: constantItemsType(constant.Type)}}, destination)
	default:
		return nil, false, errors.New("unsupported source")
	}
}

func constantTypeToSchema(kind string) string {
	if kind == "string-array" {
		return "array"
	}
	return kind
}
func constantItemsType(kind string) string {
	if kind == "string-array" {
		return "string"
	}
	return ""
}

func convertStringSource(value string, destination FeatureRequestDestination) (json.RawMessage, bool, error) {
	if destination.Type == "array" {
		item, present, err := convertStringScalar(value, destination.ItemsType)
		if err != nil || !present {
			return nil, present, err
		}
		encoded, err := json.Marshal([]json.RawMessage{item})
		return encoded, true, err
	}
	return convertStringScalar(value, destination.Type)
}

func convertStringScalar(value, kind string) (json.RawMessage, bool, error) {
	switch kind {
	case "string":
		encoded, err := json.Marshal(value)
		return encoded, true, err
	case "integer":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, true, err
		}
		encoded, err := json.Marshal(parsed)
		return encoded, true, err
	case "number":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil, true, errors.New("invalid number")
		}
		encoded, err := json.Marshal(parsed)
		return encoded, true, err
	case "boolean":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, true, err
		}
		encoded, err := json.Marshal(parsed)
		return encoded, true, err
	default:
		return nil, true, errors.New("unsupported scalar type")
	}
}

func convertRequestValue(value any, source argumentSchemaProperty, destination FeatureRequestDestination) (json.RawMessage, bool, error) {
	if destination.Type == "array" {
		var values []any
		if source.Type == "array" {
			var ok bool
			values, ok = value.([]any)
			if !ok {
				return nil, true, errors.New("array source is invalid")
			}
		} else {
			values = []any{value}
		}
		items := make([]json.RawMessage, 0, len(values))
		for _, item := range values {
			converted, err := convertRequestScalar(item, destination.ItemsType)
			if err != nil {
				return nil, true, err
			}
			items = append(items, converted)
		}
		encoded, err := json.Marshal(items)
		return encoded, true, err
	}
	if source.Type == "array" {
		return nil, true, errors.New("array cannot populate scalar")
	}
	encoded, err := convertRequestScalar(value, destination.Type)
	return encoded, true, err
}

func convertRequestScalar(value any, kind string) (json.RawMessage, error) {
	switch kind {
	case "string":
		if _, ok := value.(string); !ok {
			return nil, errors.New("expected a string")
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return nil, errors.New("expected an integer")
		}
		if _, err := strconv.ParseInt(number.String(), 10, 64); err != nil {
			return nil, errors.New("expected an integer")
		}
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return nil, errors.New("expected a number")
		}
		parsed, err := strconv.ParseFloat(number.String(), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil, errors.New("expected a finite number")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return nil, errors.New("expected a boolean")
		}
	default:
		return nil, errors.New("unsupported value type")
	}
	encoded, err := json.Marshal(value)
	return encoded, err
}

func asRequestRepeatedValues(raw json.RawMessage, destination FeatureRequestDestination) ([]json.RawMessage, error) {
	if destination.Type == "array" {
		value, err := decodeJSONValue(raw)
		if err != nil {
			return nil, err
		}
		items, ok := value.([]any)
		if !ok {
			return nil, errors.New("array destination is invalid")
		}
		out := make([]json.RawMessage, 0, len(items))
		for _, item := range items {
			encoded, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			out = append(out, encoded)
		}
		return out, nil
	}
	return []json.RawMessage{append(json.RawMessage(nil), raw...)}, nil
}

func resolveRequestPath(template string, pathValues map[string]json.RawMessage) (string, error) {
	if err := validateRequestPathTemplate(template); err != nil {
		return "", err
	}
	segments := strings.Split(template[1:], "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			key := segment[1 : len(segment)-1]
			raw, ok := pathValues[key]
			if !ok {
				return "", fmt.Errorf("path binding %q is missing", key)
			}
			var value string
			if json.Unmarshal(raw, &value) != nil || value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\") {
				return "", fmt.Errorf("path binding %q is invalid", key)
			}
			encoded := url.PathEscape(value)
			if encoded == "." {
				encoded = "%2E"
			}
			segments[index] = encoded
		}
	}
	return "/" + strings.Join(segments, "/"), nil
}

func decodeJSONValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := requireJSONEnd(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func validateValueForType(value any, kind, itemsType string) error {
	if kind == "array" || kind == "string-array" {
		if kind == "string-array" {
			itemsType = "string"
		}
		items, ok := value.([]any)
		if !ok {
			return errors.New("value must be an array")
		}
		for _, item := range items {
			if _, err := convertRequestScalar(item, itemsType); err != nil {
				return errors.New("array items have an invalid type")
			}
		}
		return nil
	}
	if kind == "string-array" {
		return errors.New("invalid array type")
	}
	if _, err := convertRequestScalar(value, kind); err != nil {
		return fmt.Errorf("value must be %s", kind)
	}
	return nil
}
