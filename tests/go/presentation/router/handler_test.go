package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/base/admin/auth"
)

type fakeRouter struct {
	authCalls        int
	listInput        engine.ListRoutesRequest
	createInput      engine.CreateRouteRequest
	getRoute         engine.Route
	replaceInput     engine.ReplaceRouteTargetRequest
	replaceCalls     int
	deliveryInput    engine.DeliveryRequest
	createRoute      engine.Route
	delivery         engine.DeliveryResponse
	authErr          error
	deliverErr       error
	sessionAuthCalls int
}

func validTestConfig() Config {
	return Config{BranchID: "storefront", MaxJSONBytes: 4096, DefaultPageSize: 50, MaxPageSize: 100}
}

func (f *fakeRouter) Authenticate(ctx context.Context, _ string) (context.Context, error) {
	f.authCalls++
	if f.authErr != nil {
		return nil, f.authErr
	}
	return context.WithValue(ctx, struct{}{}, "actor"), nil
}
func (f *fakeRouter) AuthenticateSession(ctx context.Context, _ string) (context.Context, error) {
	f.sessionAuthCalls++
	return context.WithValue(ctx, struct{}{}, "session actor"), nil
}
func (f *fakeRouter) ListRoutes(_ context.Context, input engine.ListRoutesRequest) (engine.RoutePage, error) {
	f.listInput = input
	return engine.RoutePage{Items: []engine.Route{}}, nil
}
func (f *fakeRouter) GetRoute(context.Context, string) (engine.Route, error) {
	return f.getRoute, nil
}
func (f *fakeRouter) CreateRoute(_ context.Context, input engine.CreateRouteRequest) (engine.Route, error) {
	f.createInput = input
	return f.createRoute, nil
}
func (f *fakeRouter) ReplaceRouteTarget(_ context.Context, input engine.ReplaceRouteTargetRequest) (engine.Route, error) {
	f.replaceCalls++
	f.replaceInput = input
	return engine.Route{ID: input.RouteID, BranchID: "storefront", Revision: input.ExpectedRevision + 1}, nil
}
func (*fakeRouter) DeleteRoute(context.Context, engine.DeleteRouteRequest) error { return nil }
func (f *fakeRouter) Deliver(_ context.Context, input engine.DeliveryRequest) (engine.DeliveryResponse, error) {
	f.deliveryInput = input
	return f.delivery, f.deliverErr
}

func TestSettingsAPIAuthenticatesAndPinsConfiguredBranch(t *testing.T) {
	api := &fakeRouter{createRoute: engine.Route{ID: "route-id", BranchID: "storefront", Revision: 1, State: "active"}}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/router/routes?host=site.example", nil)
	request.Header.Set("Authorization", "Bearer admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.authCalls != 1 {
		t.Fatalf("status=%d auth=%d body=%s", response.Code, api.authCalls, response.Body.String())
	}
	if api.listInput.BranchID != "storefront" || api.listInput.Host != "site.example" {
		t.Fatalf("request escaped configured branch: %+v", api.listInput)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe Router response headers: %v", response.Header())
	}
}

func TestListPublicOperationsReturnsOnlyCompiledCapabilities(t *testing.T) {
	operation := validPublicOperation("public.form-upload")
	operation.descriptor.MaxRequestBytes = 4096
	registry, err := NewPublicOperationRegistry(operation)
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeRouter{}
	config := validTestConfig()
	config.PublicOperations = registry
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/admin/router/operations", nil)
	request.Header.Set("Authorization", "Bearer admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page struct {
		Items []engine.PublicOperationDescriptor `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || api.authCalls != 1 || len(page.Items) != 1 || page.Items[0].ID != "public.form-upload" ||
		page.Items[0].MaxRequestBytes != 4096 || len(page.Items[0].AllowedContentTypes) != 1 || page.Items[0].AllowedContentTypes[0] != "application/octet-stream" {
		t.Fatalf("unexpected operation catalog response: status=%d auth=%d page=%+v body=%s", response.Code, api.authCalls, page, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("operation catalog response is missing security headers: %v", response.Header())
	}
}

func TestUnsupportedAdminMethodDoesNotFallThroughToPublicDelivery(t *testing.T) {
	api := &fakeRouter{}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, operationsPath, strings.NewReader(`{"unexpected":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound || api.deliveryInput.Method != "" {
		t.Fatalf("unsupported admin request reached public delivery: status=%d request=%+v body=%s", response.Code, api.deliveryInput, response.Body.String())
	}
}

func TestEncodedAdminPathDoesNotFallThroughToPublicDelivery(t *testing.T) {
	api := &fakeRouter{}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/%61pi/v2/admin/router/operations", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized && response.Code != http.StatusNotFound {
		t.Fatalf("encoded admin path was not rejected by the admin boundary: status=%d body=%s", response.Code, response.Body.String())
	}
	if api.deliveryInput.Method != "" {
		t.Fatalf("encoded admin path reached public delivery: request=%+v", api.deliveryInput)
	}
}

func TestCreateCoreFileRouteRequiresExactPathMatch(t *testing.T) {
	api := &fakeRouter{}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(`{"host":"site.example","path":"/download","match":"prefix","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}}`))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Idempotency-Key", "router-route-create-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.createInput.Target.Kind != "" {
		t.Fatalf("prefix file route was accepted or reached engine: status=%d input=%+v body=%s", response.Code, api.createInput, response.Body.String())
	}
}

func TestCreateRegisteredOperationRouteRequiresExactPathMatch(t *testing.T) {
	api := &fakeRouter{}
	operations, err := NewPublicOperationRegistry(validPublicOperation("public.form-session"))
	if err != nil {
		t.Fatal(err)
	}
	config := validTestConfig()
	config.PublicOperations = operations
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"host":"site.example","path":"/forms","match":"prefix","target":{"kind":"registered-operation","operation_id":"public.form-session","operation_config":{}}}`
	request := httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Idempotency-Key", "router-route-prefix-op-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.createInput.Target.Kind != "" {
		t.Fatalf("prefix operation route was accepted or reached engine: status=%d input=%+v body=%s", response.Code, api.createInput, response.Body.String())
	}
}

func TestCreateRouteRejectsUnknownMatchMode(t *testing.T) {
	api := &fakeRouter{}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(`{"host":"site.example","path":"/download","match":"regex","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}}`))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Idempotency-Key", "router-route-create-0002")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.createInput.Target.Kind != "" {
		t.Fatalf("unknown route match was accepted or reached engine: status=%d input=%+v body=%s", response.Code, api.createInput, response.Body.String())
	}
}

func TestReplaceTargetCannotBindCoreFileToPrefixRoute(t *testing.T) {
	const routeID = "01938f7a-68af-7c10-a51d-78b6ca125301"
	api := &fakeRouter{getRoute: engine.Route{ID: routeID, BranchID: "storefront", Match: "prefix", Revision: 4}}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, routesPath+"/"+routeID+"/target", strings.NewReader(`{"target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125302"}}`))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("If-Match", `"4"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.replaceCalls != 0 {
		t.Fatalf("prefix route accepted a core-file target: status=%d replace_calls=%d body=%s", response.Code, api.replaceCalls, response.Body.String())
	}
}

func TestReplaceTargetRejectsAmbiguousIfMatchValues(t *testing.T) {
	const routeID = "01938f7a-68af-7c10-a51d-78b6ca125301"
	for _, values := range [][]string{{`"4"`, `"5"`}, {`""4""`}, {"4"}, {`"04"`}} {
		api := &fakeRouter{getRoute: engine.Route{ID: routeID, BranchID: "storefront", Match: "exact", Revision: 4}}
		handler, err := NewHandler(api, validTestConfig())
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, routesPath+"/"+routeID+"/target", strings.NewReader(`{"target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125302"}}`))
		request.Header.Set("Authorization", "Bearer admin-secret")
		request.Header.Set("Content-Type", "application/json")
		for _, value := range values {
			request.Header.Add("If-Match", value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || api.replaceCalls != 0 {
			t.Fatalf("If-Match values %q were accepted: status=%d replace_calls=%d body=%s", values, response.Code, api.replaceCalls, response.Body.String())
		}
	}
}

func TestListRoutesRejectsUnknownAndRepeatedQueryKeys(t *testing.T) {
	for _, target := range []string{routesPath + "?branch_id=attacker", routesPath + "?limit=5&limit=10"} {
		api := &fakeRouter{}
		handler, err := NewHandler(api, validTestConfig())
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || api.authCalls != 1 || api.listInput.BranchID != "" {
			t.Fatalf("query %q was not rejected before engine call: status=%d auth=%d input=%+v", target, response.Code, api.authCalls, api.listInput)
		}
	}
}

func TestListRoutesUsesConfiguredPaginationBounds(t *testing.T) {
	api := &fakeRouter{}
	config := validTestConfig()
	config.DefaultPageSize, config.MaxPageSize = 7, 9
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, routesPath, nil)
	request.Header.Set("Authorization", "Bearer admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.listInput.Limit != 7 {
		t.Fatalf("default page size = %d, status=%d", api.listInput.Limit, response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, routesPath+"?limit=10", nil)
	request.Header.Set("Authorization", "Bearer admin-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.listInput.Limit != 7 {
		t.Fatalf("out-of-range page size reached engine: status=%d limit=%d", response.Code, api.listInput.Limit)
	}
}

func TestNewHandlerRequiresExplicitLimitsAndNonNilAPI(t *testing.T) {
	config := validTestConfig()
	config.DefaultPageSize = 0
	if _, err := NewHandler(&fakeRouter{}, config); err == nil {
		t.Fatal("missing Router pagination defaults accepted")
	}
	config = validTestConfig()
	var api *fakeRouter
	if _, err := NewHandler(api, config); err == nil {
		t.Fatal("typed nil Router API accepted")
	}
}

func TestSettingsAPIAcceptsCookieSessionAndProtectsWrites(t *testing.T) {
	api := &fakeRouter{createRoute: engine.Route{ID: "route-id", BranchID: "storefront", Revision: 1, State: "active"}}
	config := validTestConfig()
	config.AllowedOrigins = []string{"https://admin.example"}
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}

	read := httptest.NewRequest(http.MethodGet, routesPath, nil)
	read.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + strings.Repeat("A", 43)})
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK || api.sessionAuthCalls != 1 {
		t.Fatalf("cookie read status=%d session auth=%d body=%s", readResponse.Code, api.sessionAuthCalls, readResponse.Body.String())
	}

	write := httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(`{"host":"site.example","path":"/","match":"exact","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}}`))
	write.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + strings.Repeat("A", 43)})
	write.AddCookie(&http.Cookie{Name: adminhttpauth.CSRFCookieName, Value: strings.Repeat("A", 43)})
	write.Header.Set("Origin", "https://admin.example")
	write.Header.Set("X-CSRF-Token", strings.Repeat("A", 43))
	write.Header.Set("Idempotency-Key", "router-route-create-cookie-0001")
	write.Header.Set("Content-Type", "application/json")
	writeResponse := httptest.NewRecorder()
	handler.ServeHTTP(writeResponse, write)
	if writeResponse.Code != http.StatusCreated || api.sessionAuthCalls != 2 {
		t.Fatalf("cookie write status=%d session auth=%d body=%s", writeResponse.Code, api.sessionAuthCalls, writeResponse.Body.String())
	}

	for name, mutation := range map[string]func(*http.Request){
		"untrusted origin": func(request *http.Request) { request.Header.Set("Origin", "https://evil.example") },
		"csrf mismatch":    func(request *http.Request) { request.Header.Set("X-CSRF-Token", strings.Repeat("B", 43)) },
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodDelete, routesPath+"/01938f7a-68af-7c10-a51d-78b6ca125301", nil)
			request.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + strings.Repeat("A", 43)})
			request.AddCookie(&http.Cookie{Name: adminhttpauth.CSRFCookieName, Value: strings.Repeat("A", 43)})
			request.Header.Set("Origin", "https://admin.example")
			request.Header.Set("X-CSRF-Token", strings.Repeat("A", 43))
			mutation(request)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("invalid browser write was accepted: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCreateRouteAcceptsExplicitCoreTargets(t *testing.T) {
	api := &fakeRouter{createRoute: engine.Route{ID: "route-id", State: "active"}}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/router/routes", strings.NewReader(`{"host":"site.example","path":"/","match":"exact","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}}`))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Idempotency-Key", "router-route-create-0001")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if api.createInput.BranchID != "storefront" || api.createInput.Target.Kind != engine.TargetCoreFile || api.createInput.IdempotencyKey != "router-route-create-0001" {
		t.Fatalf("unexpected route request: %+v", api.createInput)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", response.Header().Get("Cache-Control"))
	}
	request = httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(`{"branch_id":"storefront","host":"site.example","path":"/","match":"exact","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}}`))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Idempotency-Key", "router-route-create-0002")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("client-supplied branch_id was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCreateRouteAcceptsRegisteredOperationTarget(t *testing.T) {
	uploadConfig := `{"access_group":"forms.uploads","visibility":"private","max_bytes":1048576}`
	api := &fakeRouter{createRoute: engine.Route{ID: "route-id", BranchID: "storefront", State: "active", Revision: 1}}
	operations, err := NewPublicOperationRegistry(validPublicOperation("public.form-upload"))
	if err != nil {
		t.Fatal(err)
	}
	config := validTestConfig()
	config.PublicOperations = operations
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"host":"site.example","path":"/upload","match":"exact","target":{"kind":"registered-operation","operation_id":"public.form-upload","operation_config":` + uploadConfig + `}}`
	request := httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Idempotency-Key", "router-route-create-op-0001")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || api.createInput.Target.OperationID != "public.form-upload" || string(api.createInput.Target.OperationConfig) != uploadConfig {
		t.Fatalf("registered operation route was not accepted: status=%d input=%+v body=%s", response.Code, api.createInput, response.Body.String())
	}
}

func TestCreateRouteRejectsRegisteredOperationMissingFromBuild(t *testing.T) {
	api := &fakeRouter{}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"host":"site.example","path":"/upload","match":"exact","target":{"kind":"registered-operation","operation_id":"public.form-upload","operation_config":{}}}`
	request := httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Idempotency-Key", "router-route-create-op-0001")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.createInput.Target.Kind != "" {
		t.Fatalf("unregistered operation route was accepted: status=%d input=%+v body=%s", response.Code, api.createInput, response.Body.String())
	}
}

func TestCreateRouteRejectsHandlersAndUnknownFields(t *testing.T) {
	for _, body := range []string{
		`{"host":"site.example","path":"/","match":"exact","target":{"kind":"handler","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}}`,
		`{"host":"site.example","path":"/","match":"exact","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"},"handler":"evil"}`,
	} {
		api := &fakeRouter{}
		handler, err := NewHandler(api, validTestConfig())
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/admin/router/routes", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer admin-secret")
		request.Header.Set("Idempotency-Key", "router-route-create-0001")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || api.createInput.Target.Kind != "" {
			t.Fatalf("status=%d create=%+v body=%s", response.Code, api.createInput, response.Body.String())
		}
	}
}

func TestCreateRouteRejectsApplicationJsonLookalikeContentType(t *testing.T) {
	api := &fakeRouter{}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(`{"host":"site.example"}`))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Idempotency-Key", "router-route-create-0001")
	request.Header.Set("Content-Type", "application/jsonp")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.createInput.Target.Kind != "" {
		t.Fatalf("lookalike Content-Type accepted: status=%d input=%+v body=%s", response.Code, api.createInput, response.Body.String())
	}
}

func TestCreateRouteRejectsAmbiguousIdempotencyKeyHeaders(t *testing.T) {
	api := &fakeRouter{}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(`{"host":"site.example","path":"/","match":"exact","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}}`))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Add("Idempotency-Key", "router-route-create-0001")
	request.Header.Add("Idempotency-Key", "router-route-create-0002")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.createInput.IdempotencyKey != "" {
		t.Fatalf("ambiguous idempotency headers reached route creation: status=%d input=%+v body=%s", response.Code, api.createInput, response.Body.String())
	}
}

func TestCreateRouteRejectsDuplicateContentTypeHeaders(t *testing.T) {
	api := &fakeRouter{}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(`{"host":"site.example","path":"/","match":"exact","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"}}`))
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Idempotency-Key", "router-route-create-0001")
	request.Header.Add("Content-Type", "application/json")
	request.Header.Add("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.createInput.Target.Kind != "" {
		t.Fatalf("ambiguous Content-Type reached route creation: status=%d input=%+v body=%s", response.Code, api.createInput, response.Body.String())
	}
}

func TestCreateRouteRejectsDuplicateJSONKeysAndInvalidUTF8(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"host":"site.example","path":"/","match":"exact","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301","kind":"registered-operation"}}`),
		append([]byte(`{"host":"site.example","path":"/","match":"exact","target":{"kind":"core-file","file_id":"01938f7a-68af-7c10-a51d-78b6ca125301"},"x":"`), 0xff, '"', '}'),
	} {
		api := &fakeRouter{}
		handler, err := NewHandler(api, validTestConfig())
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, routesPath, strings.NewReader(string(body)))
		request.Header.Set("Authorization", "Bearer admin-secret")
		request.Header.Set("Idempotency-Key", "router-route-create-0001")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || api.createInput.Target.Kind != "" {
			t.Fatalf("ambiguous JSON reached route creation: status=%d input=%+v body=%s", response.Code, api.createInput, response.Body.String())
		}
	}
}

func TestDeliveryUsesConfiguredBranchAndIgnoresForwardedHost(t *testing.T) {
	api := &fakeRouter{delivery: engine.DeliveryResponse{StatusCode: http.StatusOK, ContentType: "text/html; charset=utf-8", ContentLength: 5, Body: io.NopCloser(strings.NewReader("hello"))}}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://public.example/catalog?sort=asc", nil)
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	request.Header.Set("Range", "bytes=0-4")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "hello" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if api.deliveryInput.BranchID != "storefront" || api.deliveryInput.Host != "public.example" || api.deliveryInput.EscapedPath != "/catalog" || api.deliveryInput.Query != "sort=asc" || api.deliveryInput.Range != "bytes=0-4" {
		t.Fatalf("unexpected delivery input: %+v", api.deliveryInput)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe delivery headers: %v", response.Header())
	}
}

func TestDeliveryForwardsPublicOperationBodyAndIdempotencyKey(t *testing.T) {
	api := &fakeRouter{delivery: engine.DeliveryResponse{StatusCode: http.StatusCreated, ContentType: "application/json", ContentLength: int64(len(`{"ok":1}`)), Body: io.NopCloser(strings.NewReader(`{"ok":1}`))}}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://public.example/upload", strings.NewReader("filedata"))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Idempotency-Key", "public-upload-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	requestBody, err := io.ReadAll(api.deliveryInput.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || response.Body.String() != `{"ok":1}` || api.deliveryInput.Method != http.MethodPost || string(requestBody) != "filedata" ||
		api.deliveryInput.ContentType != "application/octet-stream" || api.deliveryInput.ContentLength != int64(len("filedata")) || api.deliveryInput.IdempotencyKey != "public-upload-request-0001" {
		t.Fatalf("public operation request was not forwarded safely: status=%d request=%+v body=%q response=%q", response.Code, api.deliveryInput, requestBody, response.Body.String())
	}
}

func TestDeliveryRejectsAmbiguousOperationHeaders(t *testing.T) {
	for _, test := range []struct {
		name   string
		header string
		values []string
	}{
		{name: "content type", header: "Content-Type", values: []string{"application/octet-stream", "text/plain"}},
		{name: "idempotency key", header: "Idempotency-Key", values: []string{"public-upload-request-0001", "public-upload-request-0002"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &fakeRouter{}
			handler, err := NewHandler(api, validTestConfig())
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "https://public.example/upload", strings.NewReader("filedata"))
			for _, value := range test.values {
				request.Header.Add(test.header, value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || api.deliveryInput.Method != "" {
				t.Fatalf("ambiguous header reached Router: status=%d input=%+v body=%s", response.Code, api.deliveryInput, response.Body.String())
			}
		})
	}
}

func TestDeliveryDoesNotCacheAndDoesNotLeakEngineErrors(t *testing.T) {
	api := &fakeRouter{deliverErr: engine.ErrUnavailable}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://public.example/missing", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "database") {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestDeliveryMapsOperationBodyLimitToPayloadTooLarge(t *testing.T) {
	api := &fakeRouter{deliverErr: engine.ErrTooLarge}
	handler, err := NewHandler(api, validTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://public.example/upload", strings.NewReader("too large"))
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), `"too_large"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
