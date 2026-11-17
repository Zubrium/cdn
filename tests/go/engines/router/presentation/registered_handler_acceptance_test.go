package presentation

import (
	"encoding/json"
	"testing"
)

func TestRouterV1RejectsHandlerTargets(t *testing.T) {
	var target RouteTarget
	if err := json.Unmarshal([]byte(`{"kind":"handler","handler_id":"forms.submit"}`), &target); err != nil {
		t.Fatal(err)
	}
	if err := target.Validate(); err == nil {
		t.Fatal("Router v1 accepted a handler target")
	}
}
