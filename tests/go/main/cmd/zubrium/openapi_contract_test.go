package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"gopkg.in/yaml.v3"
)

type openAPIDocument struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Security   []map[string][]string  `yaml:"security"`
	Paths      map[string]openAPIPath `yaml:"paths"`
	Components struct {
		SecuritySchemes map[string]openAPISecurityScheme `yaml:"securitySchemes"`
		Schemas         map[string]openAPIBuildSchema    `yaml:"schemas"`
	} `yaml:"components"`
}

type openAPIParameter struct {
	Name     string `yaml:"name"`
	In       string `yaml:"in"`
	Required bool   `yaml:"required"`
	Schema   struct {
		Type      string `yaml:"type"`
		MinLength int    `yaml:"minLength"`
		MaxLength int    `yaml:"maxLength"`
	} `yaml:"schema"`
}

type openAPIBuildSchema struct {
	Type                 string               `yaml:"type"`
	Ref                  string               `yaml:"$ref"`
	AdditionalProperties bool                 `yaml:"additionalProperties"`
	Required             []string             `yaml:"required"`
	Properties           map[string]yaml.Node `yaml:"properties"`
	AllOf                []openAPIBuildSchema `yaml:"allOf"`
	OneOf                []openAPIBuildSchema `yaml:"oneOf"`
	AnyOf                []openAPIBuildSchema `yaml:"anyOf"`
}

type openAPIPath struct {
	Get     *openAPIOperation `yaml:"get"`
	Head    *openAPIOperation `yaml:"head"`
	Post    *openAPIOperation `yaml:"post"`
	Put     *openAPIOperation `yaml:"put"`
	Patch   *openAPIOperation `yaml:"patch"`
	Delete  *openAPIOperation `yaml:"delete"`
	Options *openAPIOperation `yaml:"options"`
}

type openAPIOperation struct {
	OperationID string                 `yaml:"operationId"`
	Security    *[]map[string][]string `yaml:"security"`
	Parameters  []openAPIParameter     `yaml:"parameters"`
	RequestBody *struct {
		Content map[string]struct {
			Schema openAPIBuildSchema `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"requestBody"`
	Responses  map[string]yaml.Node `yaml:"responses"`
	CSRF       bool                 `yaml:"x-csrf-required-for-cookie-auth"`
	Origin     bool                 `yaml:"x-origin-required"`
	GroupLevel *struct {
		Group string `yaml:"group"`
		Level int    `yaml:"level"`
	} `yaml:"x-required-group-level"`
	RateLimitOwner            string `yaml:"x-rate-limit-client-ip-owner"`
	RouterClientIPRateLimiter bool   `yaml:"x-router-has-client-ip-rate-limiter"`
	ExternalProxyRequired     bool   `yaml:"x-deployment-requires-external-proxy"`
	ProxyConfigurationOwner   string `yaml:"x-proxy-configuration-owner"`
	ProxyAdmissionPolicyOwner string `yaml:"x-proxy-admission-policy-owner"`
	RegisteredPublicOperation string `yaml:"x-registered-public-operation"`
}

type openAPISecurityScheme struct {
	Type   string `yaml:"type"`
	In     string `yaml:"in"`
	Name   string `yaml:"name"`
	Scheme string `yaml:"scheme"`
}

func TestOpenAPIContractsAreVersionedAndStructurallyValid(t *testing.T) {
	for _, name := range []string{
		"admin-identity.yaml", "admin-notifications.yaml", "content.yaml", "coordinator.yaml", "files.yaml", "forms.yaml", "router.yaml", "website.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			contract := loadOpenAPIContract(t, name)
			if contract.OpenAPI != "3.1.0" || contract.Info.Title == "" || !validContractVersion(contract.Info.Version) {
				t.Fatalf("contract version/header is invalid: openapi=%q info=%+v", contract.OpenAPI, contract.Info)
			}
			if len(contract.Paths) == 0 || len(contract.Security) == 0 {
				t.Fatal("contract must declare paths and global authentication")
			}
			if contract.Components.SecuritySchemes["AdminSession"] != (openAPISecurityScheme{Type: "apiKey", In: "cookie", Name: "zubrium_admin_session"}) {
				t.Fatalf("AdminSession must use the Presentation cookie transport: %+v", contract.Components.SecuritySchemes["AdminSession"])
			}
			if contract.Components.SecuritySchemes["BearerCredential"] != (openAPISecurityScheme{Type: "http", Scheme: "bearer"}) {
				t.Fatalf("BearerCredential scheme is missing or invalid: %+v", contract.Components.SecuritySchemes["BearerCredential"])
			}
			for path, item := range contract.Paths {
				for method, operation := range item.operations() {
					if operation == nil {
						continue
					}
					if operation.OperationID == "" || len(operation.Responses) == 0 {
						t.Errorf("%s %s must have operationId and responses", method, path)
					}
					// Public Router operations are compiled into the profile and
					// authenticated by the delivery boundary, not Admin cookies.
					publicOperation := operation.RegisteredPublicOperation != ""
					if operation.Security != nil && len(*operation.Security) == 0 && path != "/api/admin/session" && path != "/" && !publicOperation {
						t.Errorf("%s %s disables authentication outside the login or public router document", method, path)
					}
					if isStateChangingMethod(method) && path != "/" && !publicOperation && !operation.CSRF && !operation.Origin {
						t.Errorf("%s %s has no cookie CSRF or Origin protection declaration", method, path)
					}
				}
			}
		})
	}
}

func TestOpenAPIOperationsMatchRegisteredHTTPHandlers(t *testing.T) {
	contracts := []struct {
		name string
		src  string
	}{
		{name: "admin-identity.yaml", src: "presentation/admin/base/admin/main/handler.go"},
		{name: "admin-notifications.yaml", src: "presentation/admin/base/notification/notifications.go"},
		{name: "content.yaml", src: "presentation/admin/content/handler.go"},
		{name: "coordinator.yaml", src: "presentation/admin/base/admin/coordinator/handler.go"},
		{name: "files.yaml", src: "presentation/admin/files/handler.go"},
		{name: "forms.yaml", src: "presentation/admin/forms/handler.go"},
		{name: "router.yaml", src: "presentation/router/handler.go"},
		{name: "website.yaml", src: "presentation/admin/sites/handler.go"},
	}

	for _, item := range contracts {
		t.Run(item.name, func(t *testing.T) {
			contract := loadOpenAPIContract(t, item.name)
			documented := documentedOperations(contract)
			registered := registeredHTTPOperations(t, filepath.Join("..", "..", "..", item.src))
			for path, pathItem := range contract.Paths {
				// The public Router document describes the registered catch-all
				// dispatcher. Its matching and operation restrictions are covered
				// by Router delivery acceptance tests.
				if path == "/" {
					continue
				}
				for method, operation := range pathItem.operations() {
					if operation == nil {
						continue
					}
					operationID := method + " " + path
					if !matchesRegisteredOperation(operationID, registered) {
						t.Errorf("OpenAPI declares %s but no matching ServeMux handler is registered", operationID)
					}
				}
			}
			for operation := range registered {
				if strings.Contains(operation, "{path...}") {
					continue // catch-all pattern; explicit paths are checked above
				}
				normalized := strings.ReplaceAll(operation, "/{$}", "/")
				if !documented[normalized] {
					t.Errorf("ServeMux registers %s but %s does not document it", operation, item.name)
				}
			}
		})
	}
}

// Each published contract owns a disjoint set of canonical HTTP prefixes.
// A path outside its prefixes means one Presentation module is claiming an
// endpoint that belongs to another module.
var openAPIModulePrefixes = map[string][]string{
	"admin-identity.yaml":      {"/api/admin/session", "/api/admin/principals", "/api/admin/credentials", "/api/admin/access-groups"},
	"admin-notifications.yaml": {"/api/admin/notifications"},
	"content.yaml":             {"/api/admin/content"},
	"coordinator.yaml":         {"/api/coordinator/"},
	"files.yaml":               {"/api/files"},
	"forms.yaml":               {"/api/admin/forms"},
	"router.yaml":              {"/api/admin/router", "/api/locale-preference", "/"},
	"website.yaml":             {"/api/admin/model", "/api/admin/pages", "/api/admin/templates", "/api/admin/snapshots", "/api/admin/builds", "/api/admin/features", "/api/admin/feature-imports", "/api/admin/media"},
}

func pathOwnedByPrefixes(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix == "/" {
			if path == "/" {
				return true
			}
			continue
		}
		if path == prefix || strings.HasPrefix(path, prefix+"/") || (strings.HasSuffix(prefix, "/") && strings.HasPrefix(path, prefix)) {
			return true
		}
	}
	return false
}

func TestOpenAPIModulesOwnOnlyTheirPrefixes(t *testing.T) {
	for name, prefixes := range openAPIModulePrefixes {
		t.Run(name, func(t *testing.T) {
			contract := loadOpenAPIContract(t, name)
			for path := range contract.Paths {
				if !pathOwnedByPrefixes(path, prefixes) {
					t.Errorf("%s declares %s outside its owned prefixes %v", name, path, prefixes)
				}
			}
		})
	}
}

func TestOpenAPIContractsUseOnlyLocalReferences(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "..", "spec", "presentation", "openapi"))
	if err != nil {
		t.Fatalf("read OpenAPI contracts: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		raw := openAPIRawContract(t, entry.Name())
		var refs []string
		collectExternalRefs(raw, &refs)
		if len(refs) != 0 {
			t.Errorf("%s imports another contract: %v", entry.Name(), refs)
		}
	}
}

func TestRouterAndFormsFieldDefinitionsStayEquivalent(t *testing.T) {
	routerSchema := openAPIComponentSchema(t, "router.yaml", "PublicFieldDefinition")
	formsSchema := openAPIComponentSchema(t, "forms.yaml", "FieldDefinition")
	routerJSON, err := json.Marshal(routerSchema)
	if err != nil {
		t.Fatal(err)
	}
	formsJSON, err := json.Marshal(formsSchema)
	if err != nil {
		t.Fatal(err)
	}
	if string(routerJSON) != string(formsJSON) {
		t.Errorf("Router public field schema diverged from Forms field schema:\nrouter: %s\nforms:  %s", routerJSON, formsJSON)
	}
}

func openAPIRawContract(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "presentation", "openapi", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return document
}

func openAPIComponentSchema(t *testing.T, name, schema string) any {
	t.Helper()
	document := openAPIRawContract(t, name)
	components, ok := document["components"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no components", name)
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no component schemas", name)
	}
	value, ok := schemas[schema]
	if !ok {
		t.Fatalf("%s has no schema %s", name, schema)
	}
	return value
}

func collectExternalRefs(value any, refs *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if key == "$ref" {
				if reference, ok := item.(string); ok && !strings.HasPrefix(reference, "#/") {
					*refs = append(*refs, reference)
				}
			}
			collectExternalRefs(item, refs)
		}
	case []any:
		for _, item := range typed {
			collectExternalRefs(item, refs)
		}
	}
}

func TestAdminTransportMountsOnlyOwnedPrefixes(t *testing.T) {
	source := filepath.Join("..", "..", "..", "presentation", "admin", "base", "transport", "transport.go")
	mounts := transportMountPatterns(t, source)
	var owned []string
	for _, prefixes := range openAPIModulePrefixes {
		owned = append(owned, prefixes...)
	}
	fallbackOnly := map[string]bool{
		"/api/admin/":        true,
		"/api/admin/content": true,
		"/api/admin/forms":   true,
	}
	for pattern, notFoundOnly := range mounts {
		if !strings.HasPrefix(pattern, "/api/admin") {
			continue
		}
		if fallbackOnly[pattern] {
			if !notFoundOnly {
				t.Errorf("Admin transport must not mount an adapter on %s; only a terminal NotFoundHandler is allowed", pattern)
			}
			continue
		}
		if !pathOwnedByPrefixes(strings.TrimSuffix(pattern, "/"), owned) {
			t.Errorf("Admin transport mounts %s outside every published contract prefix", pattern)
		}
	}
}

func transportMountPatterns(t *testing.T, source string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
	if err != nil {
		t.Fatalf("parse Admin transport registrations in %s: %v", source, err)
	}
	constants := parseStringConstants(file)
	mounts := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		if !isAdminMountCall(call.Fun) {
			return true
		}
		pattern, ok := evaluateString(call.Args[0], constants, make(map[string]bool))
		if !ok {
			return true
		}
		if _, path, found := strings.Cut(pattern, " "); found {
			pattern = path
		}
		mounts[pattern] = isNotFoundHandler(call.Args[1])
		return true
	})
	return mounts
}

func parseStringConstants(file *ast.File) map[string]ast.Expr {
	constants := make(map[string]ast.Expr)
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, specification := range group.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range values.Names {
				if index < len(values.Values) {
					constants[name.Name] = values.Values[index]
				}
			}
		}
	}
	return constants
}

func isAdminMountCall(function ast.Expr) bool {
	if identifier, ok := function.(*ast.Ident); ok {
		return identifier.Name == "mount"
	}
	selector, ok := function.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return ok && receiver.Name == "mux"
}

func isNotFoundHandler(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "NotFoundHandler"
}

func TestOpenAPIOperationIDsAreUniqueAcrossModules(t *testing.T) {
	owners := make(map[string]string)
	entries, err := os.ReadDir(filepath.Join("..", "..", "..", "spec", "presentation", "openapi"))
	if err != nil {
		t.Fatalf("read OpenAPI contracts: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		contract := loadOpenAPIContract(t, entry.Name())
		for path, pathItem := range contract.Paths {
			for method, operation := range pathItem.operations() {
				if operation == nil {
					continue
				}
				if owner, exists := owners[operation.OperationID]; exists && owner != entry.Name() {
					t.Errorf("operationId %s (%s %s) is declared by both %s and %s", operation.OperationID, method, path, owner, entry.Name())
				}
				owners[operation.OperationID] = entry.Name()
			}
		}
	}
}

func documentedOperations(contract openAPIDocument) map[string]bool {
	documented := make(map[string]bool)
	for path, pathItem := range contract.Paths {
		for method, operation := range pathItem.operations() {
			if operation != nil {
				documented[method+" "+path] = true
			}
		}
	}
	return documented
}

func matchesRegisteredOperation(operation string, registered map[string]bool) bool {
	if registered[operation] {
		return true
	}
	method, path, _ := strings.Cut(operation, " ")
	if method == "HEAD" && registered["GET "+path] {
		return true // Go ServeMux routes HEAD through a GET pattern.
	}
	for pattern := range registered {
		patternMethod, prefix, found := strings.Cut(pattern, " ")
		if !found || patternMethod != method {
			continue
		}
		base, _, wildcard := strings.Cut(prefix, "{path...}")
		if wildcard && strings.HasPrefix(path, base) {
			return true
		}
	}
	return false
}

func registeredHTTPOperations(t *testing.T, source string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
	if err != nil {
		t.Fatalf("parse HTTP handler registrations in %s: %v", source, err)
	}
	constants := make(map[string]ast.Expr)
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, specification := range group.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range values.Names {
				if index < len(values.Values) {
					constants[name.Name] = values.Values[index]
				}
			}
		}
	}

	registered := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "HandleFunc" {
			return true
		}
		pattern, ok := evaluateString(call.Args[0], constants, make(map[string]bool))
		if !ok {
			return true
		}
		parts := strings.SplitN(pattern, " ", 2)
		if len(parts) != 2 {
			return true
		}
		registered[parts[0]+" "+parts[1]] = true
		return true
	})
	return registered
}

func evaluateString(expression ast.Expr, constants map[string]ast.Expr, resolving map[string]bool) (string, bool) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		result, err := strconv.Unquote(value.Value)
		return result, err == nil
	case *ast.Ident:
		constant, exists := constants[value.Name]
		if !exists || resolving[value.Name] {
			return "", false
		}
		resolving[value.Name] = true
		result, ok := evaluateString(constant, constants, resolving)
		delete(resolving, value.Name)
		return result, ok
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := evaluateString(value.X, constants, resolving)
		right, rightOK := evaluateString(value.Y, constants, resolving)
		return left + right, leftOK && rightOK
	default:
		return "", false
	}
}

func TestRouterOpenAPIRegistersUploadAndReservesPublicDeliveryBoundary(t *testing.T) {
	contract := loadOpenAPIContract(t, "router.yaml")
	accessCases := []struct {
		path   string
		method string
		level  int
	}{
		{path: "/api/admin/router/operations", method: "GET", level: 1},
		{path: "/api/admin/router/routes", method: "GET", level: 1},
		{path: "/api/admin/router/routes", method: "POST", level: 2},
		{path: "/api/admin/router/routes/{id}", method: "GET", level: 1},
		{path: "/api/admin/router/routes/{id}", method: "DELETE", level: 2},
		{path: "/api/admin/router/routes/{id}/target", method: "PUT", level: 2},
	}
	for _, test := range accessCases {
		operation := contract.Paths[test.path].operations()[test.method]
		if operation == nil || operation.GroupLevel == nil || operation.GroupLevel.Group != "router_routes" || operation.GroupLevel.Level != test.level {
			t.Errorf("%s %s must require router_routes level %d", test.method, test.path, test.level)
		}
	}
	public, ok := contract.Paths["/"]
	if !ok {
		t.Fatal("public Router delivery entrypoint must be declared")
	}
	for method, operation := range map[string]*openAPIOperation{
		"GET": public.Get, "HEAD": public.Head, "POST": public.Post,
		"PUT": public.Put, "PATCH": public.Patch, "DELETE": public.Delete,
	} {
		if operation == nil {
			t.Errorf("public Router %s entrypoint must be declared", method)
			continue
		}
		if operation.Security == nil || len(*operation.Security) != 0 {
			t.Errorf("public delivery operation %q must explicitly disable API authentication", operation.OperationID)
		}
	}
	if public.Post.RateLimitOwner != "deployer-managed-proxy" || public.Post.RouterClientIPRateLimiter ||
		!public.Post.ExternalProxyRequired || public.Post.ProxyConfigurationOwner != "deployer" || public.Post.ProxyAdmissionPolicyOwner != "deployer" {
		t.Fatalf("public upload must document the external proxy boundary and no Router rate limiter: %+v", public.Post)
	}
	if _, ok := contract.Paths["/api/admin/router/routes"]; !ok {
		t.Fatal("Router route settings API is missing")
	}
}

func TestRouterOpenAPIRouteTargetDeclaresDisposition(t *testing.T) {
	contract := loadOpenAPIContract(t, "router.yaml")
	routeTarget, ok := contract.Components.Schemas["RouteTarget"]
	if !ok {
		t.Fatal("Router contract is missing RouteTarget")
	}
	node, ok := routeTarget.Properties["disposition"]
	if !ok {
		t.Fatal("RouteTarget must declare disposition")
	}
	var schema struct {
		Type string   `yaml:"type"`
		Enum []string `yaml:"enum"`
	}
	if err := node.Decode(&schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "string" || !reflect.DeepEqual(schema.Enum, []string{"inline", "attachment"}) {
		t.Fatalf("disposition schema = %+v, want string enum [inline attachment]", schema)
	}
}

func TestAdminIdentityOpenAPIHasNoRoleOrPrincipalKindFields(t *testing.T) {
	contract := loadOpenAPIContract(t, "admin-identity.yaml")
	principal, ok := contract.Components.Schemas["Principal"]
	if !ok {
		t.Fatal("identity contract is missing the Principal schema")
	}
	for _, forbidden := range []string{"role", "kind"} {
		if _, exists := principal.Properties[forbidden]; exists {
			t.Errorf("Principal schema must not define the retired %q field", forbidden)
		}
	}
	if contract.Paths["/api/admin/session"].Post == nil {
		t.Fatal("Core-backed browser session endpoint is missing")
	}
}

func TestWebsiteBuildContractFieldsMatchGoTypes(t *testing.T) {
	contract := loadOpenAPIContract(t, "website.yaml")
	build := contract.Paths["/api/admin/builds"].Post
	if build == nil {
		t.Fatal("POST /api/admin/builds is missing from the Website contract")
	}
	var idempotencyKey openAPIParameter
	foundKey := false
	for _, parameter := range build.Parameters {
		if parameter.Name == "Idempotency-Key" && parameter.In == "header" {
			idempotencyKey, foundKey = parameter, true
		}
	}
	if !foundKey || !idempotencyKey.Required || idempotencyKey.Schema.MinLength != 16 || idempotencyKey.Schema.MaxLength != 128 {
		t.Fatalf("Idempotency-Key header must require 16..128 characters: %+v", idempotencyKey)
	}
	if build.RequestBody == nil {
		t.Fatal("POST /api/admin/builds must declare a request body")
	}
	body := build.RequestBody.Content["application/json"].Schema
	if body.Type != "object" || body.AdditionalProperties || !reflect.DeepEqual(body.Required, []string{"snapshot_id", "locale"}) || len(body.Properties) != 2 {
		t.Fatalf("build body must allow only snapshot_id and locale: %+v", body)
	}
	requestFields := jsonTagFields(reflect.TypeOf(websiteapi.BuildSnapshotRequest{}))
	for field := range body.Properties {
		if !requestFields[field] {
			t.Errorf("OpenAPI build body field %q is missing from BuildSnapshotRequest", field)
		}
	}

	buildJob := contract.Components.Schemas["BuildJob"]
	wantJobFields := []string{"id", "snapshot_id", "locale", "locale_catalog_revision", "input_digest", "state", "created_at", "updated_at"}
	if !reflect.DeepEqual(buildJob.Required, wantJobFields) {
		t.Fatalf("BuildJob required fields = %v, want %v", buildJob.Required, wantJobFields)
	}
	jobFields := jsonTagFields(reflect.TypeOf(websiteapi.BuildJob{}))
	for _, field := range wantJobFields {
		if !jobFields[field] {
			t.Errorf("OpenAPI BuildJob field %q is missing from the Go contract", field)
		}
		if _, exists := buildJob.Properties[field]; !exists {
			t.Errorf("OpenAPI BuildJob is missing required property %q", field)
		}
	}
	for field := range buildJob.Properties {
		if !jobFields[field] {
			t.Errorf("OpenAPI BuildJob property %q is missing from the Go contract", field)
		}
	}
	var digestSchema struct {
		Pattern string `yaml:"pattern"`
	}
	digestNode := buildJob.Properties["input_digest"]
	if err := digestNode.Decode(&digestSchema); err != nil || digestSchema.Pattern != `^[a-f0-9]{64}$` {
		t.Fatalf("BuildJob.input_digest must require 64 lowercase hex characters: pattern=%q error=%v", digestSchema.Pattern, err)
	}
}

func jsonTagFields(value reflect.Type) map[string]bool {
	fields := make(map[string]bool, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		tag := strings.Split(value.Field(index).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			fields[tag] = true
		}
	}
	return fields
}

func loadOpenAPIContract(t *testing.T, name string) openAPIDocument {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "presentation", "openapi", name))
	if err != nil {
		t.Fatalf("read OpenAPI contract %s: %v", name, err)
	}
	var contract openAPIDocument
	if err := yaml.Unmarshal(data, &contract); err != nil {
		t.Fatalf("parse OpenAPI contract %s: %v", name, err)
	}
	return contract
}

func (path openAPIPath) operations() map[string]*openAPIOperation {
	return map[string]*openAPIOperation{
		"GET": path.Get, "HEAD": path.Head, "POST": path.Post, "PUT": path.Put,
		"PATCH": path.Patch, "DELETE": path.Delete, "OPTIONS": path.Options,
	}
}

func isStateChangingMethod(method string) bool {
	return method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE"
}

// Contract info.version identifies the contract itself, not an HTTP path
// prefix, and modules may evolve their contracts independently.
func validContractVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return false
			}
		}
	}
	return true
}
