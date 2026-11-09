package sitebuilder

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func backendBindingManifest() Manifest {
	return Manifest{
		FormatVersion: 2,
		Key:           "profile-widget",
		Name:          "Profile widget",
		Version:       "1.0.0",
		EngineAPI:     "1",
		Runtime:       &RuntimeContract{ABI: RuntimeABIStaticModule, Entry: "dist/entry.mjs"},
		Slots:         []string{"main"},
		Arguments: json.RawMessage(`{
			"type":"object",
			"properties":{
				"slug":{"type":"string","title":"Slug"},
				"page":{"type":"integer","title":"Page"},
				"profileRequest":{"type":"object","x-editor-managed-by-site":true}
			},
			"additionalProperties":false
		}`),
		DataInputs: []DataInputPort{},
		Requests: []FeatureRequestSlot{{
			Key: "profile", Label: "Profile request", Argument: "profileRequest", DefaultPath: "/profiles/{username}",
			Destinations: []FeatureRequestDestination{
				{Kind: "path", Key: "username", Label: "Profile name", Type: "string", Sources: []string{"route", "argument"}, Required: true},
				{Kind: "query", Key: "tags", Label: "Tags", Type: "array", ItemsType: "string", Sources: []string{"query", "argument", "constant"}},
				{Kind: "body", Key: "page", Label: "Page", Type: "integer", Sources: []string{"argument", "constant"}},
				{Kind: "header", Key: "X-Language", Label: "Language", Type: "string", Sources: []string{"constant"}},
			},
		}},
	}
}

func backendBindingConfig() SiteConfig {
	backendID := "4780be1d-920f-4c8e-b697-02427aef4c9b"
	placement := Placement{
		ID:    "profile",
		Build: BuildRef{FeatureKey: "profile-widget", Version: "1.0.0"},
		Slot:  "main",
		Arguments: map[string]ArgumentBinding{
			"page": {Source: "literal", Value: json.RawMessage(`3`)},
		},
		BackendRequests: map[string]BackendRequestBinding{
			"profile": {
				BackendID: backendID,
				Path:      "/profiles/{username}",
				Bindings: []BackendValueBinding{
					{Destination: BackendValueDestination{Kind: "path", Key: "username"}, Source: BackendValueSource{Kind: "route", Key: "slug"}},
					{Destination: BackendValueDestination{Kind: "query", Key: "tags"}, Source: BackendValueSource{Kind: "query", Key: "tag"}},
					{Destination: BackendValueDestination{Kind: "body", Key: "page"}, Source: BackendValueSource{Kind: "argument", Key: "page"}},
					{Destination: BackendValueDestination{Kind: "header", Key: "X-Language"}, Source: BackendValueSource{Kind: "constant", Key: "language"}},
				},
			},
		},
	}
	return SiteConfig{
		FormatVersion:   3,
		Backends:        []PublicBackend{{ID: backendID, Name: "Public profiles", BaseURL: "https://api.example.com"}},
		PublicConstants: []PublicSnapshotConstant{{Key: "language", Label: "Site language", Type: "string", Value: json.RawMessage(`"ru"`)}},
		Pages:           []Page{{ID: "home", Name: "Home", Kind: "page", Placements: []Placement{placement}}},
		RouteTree:       RouteTreeNode{ID: "site", Type: "site", Children: []RouteTreeNode{{ID: "profile-route", Type: "path", Path: "/users/:slug", PageID: "home"}}},
	}
}

func TestValidatePublicBackendBaseURLBlocksLocalAndCredentialURLs(t *testing.T) {
	valid := []string{"https://api.example.com", "http://public-api.example.net:8080/"}
	for _, raw := range valid {
		if _, err := ValidatePublicBackendBaseURL(raw); err != nil {
			t.Errorf("valid origin %q rejected: %v", raw, err)
		}
	}
	invalid := []string{
		"file:///etc/passwd", "https://user:pass@api.example.com", "https://api.example.com/?key=x",
		"https://api.example.com/#fragment", "https://api.example.com/v1", "http://localhost:8080",
		"http://service.internal", "http://127.0.0.1:8080", "http://10.0.0.5", "http://192.168.1.10",
		"http://169.254.169.254", "http://0x7f000001", "http://127.1", "http://api.08",
		"https://api.example.com:70000", "https://api.example.com:abc",
	}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			if _, err := ValidatePublicBackendBaseURL(raw); err == nil {
				t.Errorf("unsafe or malformed origin %q was accepted", raw)
			}
		})
	}
}

func TestManifestValidatesRequestSlotContractAndManagedArgument(t *testing.T) {
	manifest := backendBindingManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatalf("valid request contract was rejected: %v", err)
	}
	invalid := []struct {
		name string
		edit func(*Manifest)
	}{
		{"unmanaged argument", func(m *Manifest) {
			m.Arguments = json.RawMessage(strings.Replace(string(m.Arguments), `,"x-editor-managed-by-site":true`, "", 1))
		}},
		{"unknown source kind", func(m *Manifest) { m.Requests[0].Destinations[0].Sources = []string{"expression"} }},
		{"secret header", func(m *Manifest) { m.Requests[0].Destinations[3].Key = "Authorization" }},
		{"malformed default path", func(m *Manifest) { m.Requests[0].DefaultPath = "https://internal.example.com/{username}" }},
		{"undeclared path placeholder", func(m *Manifest) { m.Requests[0].DefaultPath = "/profiles/{other}" }},
		{"duplicate slot", func(m *Manifest) { m.Requests = append(m.Requests, m.Requests[0]) }},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			copy := backendBindingManifest()
			tc.edit(&copy)
			if err := copy.Validate(); err == nil {
				t.Fatal("invalid manifest request contract was accepted")
			}
		})
	}
}

func TestSiteConfigValidatesBackendReferencesAndRequestBindings(t *testing.T) {
	manifest := backendBindingManifest()
	lookup := compositionBuildLookup{"profile-widget@1.0.0": {Manifest: manifest}}
	cfg := backendBindingConfig()
	if err := cfg.Validate(lookup); err != nil {
		t.Fatalf("valid backend config was rejected: %v", err)
	}
	mutations := []struct {
		name string
		edit func(*SiteConfig)
	}{
		{"missing backend", func(c *SiteConfig) {
			c.Pages[0].Placements[0].BackendRequests["profile"] = BackendRequestBinding{BackendID: "6e90839d-1335-49c8-8f39-ab78ee978724", Path: "/profiles/{username}", Bindings: c.Pages[0].Placements[0].BackendRequests["profile"].Bindings}
		}},
		{"missing path capture", func(c *SiteConfig) {
			c.Pages[0].Placements[0].BackendRequests["profile"].Bindings[0].Source.Key = "other"
		}},
		{"unknown manifest slot", func(c *SiteConfig) {
			c.Pages[0].Placements[0].BackendRequests["missing"] = c.Pages[0].Placements[0].BackendRequests["profile"]
		}},
		{"removed used backend", func(c *SiteConfig) { c.Backends = nil }},
		{"sensitive public constant", func(c *SiteConfig) { c.PublicConstants[0].Key = "api_token" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			copy := backendBindingConfig()
			tc.edit(&copy)
			if err := copy.Validate(lookup); err == nil {
				t.Fatal("invalid backend binding was accepted")
			}
		})
	}
}

func TestResolveBackendRequestPreservesRepeatedQueryAndTypedMappings(t *testing.T) {
	manifest := backendBindingManifest()
	cfg := backendBindingConfig()
	placement := cfg.Pages[0].Placements[0]
	args := json.RawMessage(`{"page":3}`)
	resolved, external, err := resolvePlacementBackendRequests(manifest, cfg, placement, args, map[string]string{"slug": "a b"}, url.Values{"tag": {"first", "second"}})
	if err != nil {
		t.Fatal(err)
	}
	if !external {
		t.Fatal("registered external backend was not identified")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(resolved, &values); err != nil {
		t.Fatal(err)
	}
	var settings ResolvedBackendRequest
	if err := json.Unmarshal(values["profileRequest"], &settings); err != nil {
		t.Fatal(err)
	}
	if settings.BaseURL != "https://api.example.com" || settings.Path != "/profiles/a%20b" {
		t.Fatalf("resolved public URL = %q%s", settings.BaseURL, settings.Path)
	}
	if len(settings.Query["tags"]) != 2 {
		t.Fatalf("repeated query values were not preserved: %#v", settings.Query)
	}
	var queryValues []string
	for _, raw := range settings.Query["tags"] {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		queryValues = append(queryValues, value)
	}
	if strings.Join(queryValues, ",") != "first,second" {
		t.Fatalf("query values = %v", queryValues)
	}
	if string(settings.Body["page"]) != "3" || settings.Headers["X-Language"] != "ru" {
		t.Fatalf("typed destinations were not resolved: body=%s headers=%v", settings.Body["page"], settings.Headers)
	}
}

func TestResolveBackendRequestDistinguishesEmptyMissingAndInvalidValues(t *testing.T) {
	manifest := backendBindingManifest()
	cfg := backendBindingConfig()
	placement := cfg.Pages[0].Placements[0]
	placement.BackendRequests["profile"] = BackendRequestBinding{
		Path: "/profiles/{username}",
		Bindings: []BackendValueBinding{
			{Destination: BackendValueDestination{Kind: "path", Key: "username"}, Source: BackendValueSource{Kind: "route", Key: "slug"}},
			{Destination: BackendValueDestination{Kind: "query", Key: "tags"}, Source: BackendValueSource{Kind: "query", Key: "tag"}},
		},
	}
	for _, tc := range []struct {
		name  string
		path  map[string]string
		query url.Values
		want  string
		count int
	}{
		{name: "missing query source", path: map[string]string{"slug": "user"}, query: url.Values{}, want: "missing_source"},
		{name: "explicit empty query is retained", path: map[string]string{"slug": "user"}, query: url.Values{"tag": {""}}, want: "", count: 1},
		{name: "numeric query rejects invalid input", path: map[string]string{"slug": "user"}, query: url.Values{"tag": {"one", "two"}}, want: "", count: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved, _, err := resolvePlacementBackendRequests(manifest, cfg, placement, json.RawMessage(`{}`), tc.path, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			var arguments map[string]json.RawMessage
			if err := json.Unmarshal(resolved, &arguments); err != nil {
				t.Fatal(err)
			}
			var settings ResolvedBackendRequest
			if err := json.Unmarshal(arguments["profileRequest"], &settings); err != nil {
				t.Fatal(err)
			}
			if settings.Error != tc.want {
				t.Fatalf("error = %q, want %q", settings.Error, tc.want)
			}
			if tc.count > 0 && len(settings.Query["tags"]) != tc.count {
				t.Fatalf("array length = %d", len(settings.Query["tags"]))
			}
		})
	}
	placement.BackendRequests["profile"].Bindings[1].Destination = BackendValueDestination{Kind: "query", Key: "page"}
	placement.BackendRequests["profile"].Bindings[1].Source = BackendValueSource{Kind: "query", Key: "page"}
	manifest.Requests[0].Destinations[1].Type = "integer"
	manifest.Requests[0].Destinations[1].ItemsType = ""
	resolved, _, err := resolvePlacementBackendRequests(manifest, cfg, placement, json.RawMessage(`{}`), map[string]string{"slug": "user"}, url.Values{"page": {"not-a-number"}})
	if err != nil {
		t.Fatal(err)
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(resolved, &arguments); err != nil {
		t.Fatal(err)
	}
	var settings ResolvedBackendRequest
	if err := json.Unmarshal(arguments["profileRequest"], &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Error != "invalid_source" {
		t.Fatalf("invalid numeric source error = %q", settings.Error)
	}
}

func TestResolveRequestPathEscapesAndRejectsUnsafeSegments(t *testing.T) {
	for input, want := range map[string]string{
		"hello world": "/profiles/hello%20world",
		"100%":        "/profiles/100%25",
		"тест":        "/profiles/%D1%82%D0%B5%D1%81%D1%82",
	} {
		got, err := resolveRequestPath("/profiles/{name}", map[string]json.RawMessage{"name": json.RawMessage(mustJSON(t, input))})
		if err != nil || got != want {
			t.Errorf("resolveRequestPath(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", ".", "..", "a/b", `a\b`} {
		if _, err := resolveRequestPath("/profiles/{name}", map[string]json.RawMessage{"name": json.RawMessage(mustJSON(t, input))}); err == nil {
			t.Errorf("unsafe path segment %q was accepted", input)
		}
	}
}

func TestPublicBackendOriginsComeOnlyFromUsedManifestSlots(t *testing.T) {
	config := backendBindingConfig()
	config.Backends = append(config.Backends, PublicBackend{ID: "6e90839d-1335-49c8-8f39-ab78ee978724", Name: "Unused", BaseURL: "https://unused.example.net"})
	placement := config.Pages[0].Placements[0]
	placement.BackendRequests["undeclared"] = BackendRequestBinding{BackendID: "6e90839d-1335-49c8-8f39-ab78ee978724", Path: "/ignored"}
	got := publicBackendOriginsForPlacement(backendBindingManifest(), config, placement)
	if len(got) != 1 || got[0] != "https://api.example.com" {
		t.Fatalf("external CSP origins = %v, want only the origin used by a manifest slot", got)
	}

	sameOrigin := placement
	sameOrigin.BackendRequests = map[string]BackendRequestBinding{
		"profile": {Path: "/profiles/{username}", Bindings: placement.BackendRequests["profile"].Bindings},
	}
	if got := publicBackendOriginsForPlacement(backendBindingManifest(), config, sameOrigin); len(got) != 0 {
		t.Fatalf("same-origin binding added external CSP origins: %v", got)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
