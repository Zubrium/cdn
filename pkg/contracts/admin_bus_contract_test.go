package contracts

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdminBusMessagesCarryServerOnlyFields(t *testing.T) {
	contentMessage := AdminContentSaveWorkspaceDraftRequestMessage{
		Subject: AuthorizationSubject{Credential: "credential"},
		Request: AdminContentSaveWorkspaceDraftRequest{
			SnapshotID:       "server-only",
			IdempotencyKey:   "idem-1234567890",
			ExpectedRevision: 4,
		},
		SnapshotID:     "server-only",
		IdempotencyKey: "idem-1234567890",
	}
	payload, err := json.Marshal(contentMessage)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AdminContentSaveWorkspaceDraftRequestMessage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SnapshotID != "server-only" || decoded.IdempotencyKey != "idem-1234567890" {
		t.Fatalf("bus message lost server-only fields: %+v", decoded)
	}
	clientPayload, err := json.Marshal(contentMessage.Request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(clientPayload), "server-only") || strings.Contains(string(clientPayload), "idem-1234567890") {
		t.Fatalf("client DTO exposed server-only fields: %s", clientPayload)
	}

	websiteMessage := AdminWebsiteMessageRequest{
		Subject:          AuthorizationSubject{Credential: "credential"},
		Request:          json.RawMessage(`{}`),
		IdempotencyKey:   "idem-abcdefghijkl",
		PathID:           "template-id",
		PageID:           "page-id",
		ExpectedRevision: 7,
	}
	payload, err = json.Marshal(websiteMessage)
	if err != nil {
		t.Fatal(err)
	}
	var decodedWebsite AdminWebsiteMessageRequest
	if err := json.Unmarshal(payload, &decodedWebsite); err != nil {
		t.Fatal(err)
	}
	if decodedWebsite.IdempotencyKey != "idem-abcdefghijkl" || decodedWebsite.PathID != "template-id" ||
		decodedWebsite.PageID != "page-id" || decodedWebsite.ExpectedRevision != 7 {
		t.Fatalf("website bus message lost server-only fields: %+v", decodedWebsite)
	}

	filesMessage := AdminFilesBeginUploadRequest{
		Subject:        AuthorizationSubject{Credential: "credential"},
		Input:          AdminFileBeginUploadInput{IdempotencyKey: "idem-upload-123456"},
		IdempotencyKey: "idem-upload-123456",
	}
	payload, err = json.Marshal(filesMessage)
	if err != nil {
		t.Fatal(err)
	}
	var decodedFiles AdminFilesBeginUploadRequest
	if err := json.Unmarshal(payload, &decodedFiles); err != nil {
		t.Fatal(err)
	}
	if decodedFiles.IdempotencyKey != "idem-upload-123456" {
		t.Fatalf("files bus message lost idempotency key: %+v", decodedFiles)
	}
}

func TestAdminEngineOperationKeysAreVersionedAndUnique(t *testing.T) {
	keys := append(append(append([]Key{}, AdminContentOperationKeys()...), AdminFormsOperationKeys()...),
		append(AdminWebsiteOperationKeys(), AdminFilesOperationKeys()...)...)
	seen := make(map[Key]struct{}, len(keys))
	for _, key := range keys {
		if err := key.Validate(); err != nil {
			t.Fatalf("invalid key %+v: %v", key, err)
		}
		if key.Version != 1 {
			t.Fatalf("unexpected version for %s: %d", key.ID, key.Version)
		}
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate key %+v", key)
		}
		seen[key] = struct{}{}
	}
}

func TestValidAdminFileIdempotencyKey(t *testing.T) {
	if !ValidAdminFileIdempotencyKey("upload-1234567890:part-1") {
		t.Fatal("valid key rejected")
	}
	for _, value := range []string{"", "short", strings.Repeat("x", 129), "bad key"} {
		if ValidAdminFileIdempotencyKey(value) {
			t.Fatalf("invalid key accepted: %q", value)
		}
	}
}
