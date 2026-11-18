package presentation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRouterAcceptsTypedRegisteredOperationTarget(t *testing.T) {
	config := json.RawMessage(`{"access_group":"forms.uploads","visibility":"private","max_bytes":1048576}`)
	target := RouteTarget{Kind: TargetRegisteredOperation, OperationID: "public.form-upload", OperationConfig: config}
	if err := target.Validate(); err != nil {
		t.Fatalf("registered operation target rejected: %v", err)
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RouteTarget
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Validate() != nil || decoded.OperationID != target.OperationID {
		t.Fatalf("registered operation target did not round-trip: target=%+v err=%v", decoded, err)
	}
}

func TestRouterRejectsUnregisteredHandlerTargetAndMalformedOperationTarget(t *testing.T) {
	var target RouteTarget
	if err := json.Unmarshal([]byte(`{"kind":"handler","handler_id":"forms.submit"}`), &target); err != nil {
		t.Fatal(err)
	}
	if err := target.Validate(); err == nil {
		t.Fatal("Router accepted an unregistered handler target")
	}
	for _, target := range []RouteTarget{
		{Kind: TargetRegisteredOperation, OperationID: "", OperationConfig: json.RawMessage(`{}`)},
		{Kind: TargetRegisteredOperation, OperationID: "public/evil", OperationConfig: json.RawMessage(`{}`)},
		{Kind: TargetRegisteredOperation, OperationID: "public.form-upload", FileID: "01938f7a-68af-7c10-a51d-78b6ca125301", OperationConfig: json.RawMessage(`{}`)},
		{Kind: TargetRegisteredOperation, OperationID: "public.form-upload", OperationConfig: json.RawMessage(`[]`)},
		{Kind: TargetRegisteredOperation, OperationID: "public.form-upload", OperationConfig: json.RawMessage(strings.Repeat(" ", maxPublicOperationConfigBytes+1))},
	} {
		if err := target.Validate(); err == nil {
			t.Errorf("malformed registered operation target was accepted: %#v", target)
		}
	}
}
