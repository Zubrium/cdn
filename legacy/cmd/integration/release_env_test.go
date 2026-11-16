package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"websiteengine/pkg/environment"
)

func (e *testEnv) requestOn(base, method, path, token string, body any) (int, map[string]any, http.Header) {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 && resp.Header.Get("Content-Type") != "" {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out, resp.Header
}

// public preserves concise test call sites while mapping old aliases to the
// UUID-backed branch fixtures used by the integration environment.
func (e *testEnv) public(branch environment.Environment, method, path string, body any) (int, map[string]any, http.Header) {
	e.t.Helper()
	base := e.devServer.URL
	if branch == e.prodBranch {
		base = e.prodServer.URL
	}
	return e.requestOn(base, method, path, "", body)
}
