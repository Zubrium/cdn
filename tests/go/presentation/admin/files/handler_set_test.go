package fileapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFileAPICreatesASetThroughLoader(t *testing.T) {
	loader := newFakeLoader()
	handler := testHandler(t, loader)
	response := request(handler, http.MethodPost, apiPrefix+"/sets", `{"namespace":"features","name":"catalog assets","managementGroup":"features"}`, true)
	if response.Code != http.StatusCreated || loader.lastSetInput.ManagementGroup != "features" || loader.setCreateCalls != 1 {
		t.Fatalf("create file set: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFileAPISetMutationUsesSetRevision(t *testing.T) {
	loader := newFakeLoader()
	handler := testHandler(t, loader)
	setID := loader.set.ID
	path := apiPrefix + "/sets/" + setID
	requestWithoutETag := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"name":"renamed"}`))
	requestWithoutETag.Header.Set("Authorization", "Bearer test-core-key")
	requestWithoutETag.Header.Set("Content-Type", "application/json")
	missingTag := httptest.NewRecorder()
	handler.ServeHTTP(missingTag, requestWithoutETag)
	if missingTag.Code != http.StatusPreconditionRequired || loader.setUpdateCalls != 0 {
		t.Fatalf("missing set revision: status=%d updateCalls=%d body=%s", missingTag.Code, loader.setUpdateCalls, missingTag.Body.String())
	}
	valid := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"name":"renamed"}`))
	valid.Header.Set("Authorization", "Bearer test-core-key")
	valid.Header.Set("Content-Type", "application/json")
	valid.Header.Set("If-Match", `"`+setID+`-r4"`)
	updated := httptest.NewRecorder()
	handler.ServeHTTP(updated, valid)
	if updated.Code != http.StatusOK || loader.setUpdateCalls != 1 || loader.lastSetRevision != 4 || updated.Header().Get("ETag") != `"`+setID+`-r5"` {
		t.Fatalf("set update: status=%d revision=%d etag=%q body=%s", updated.Code, loader.lastSetRevision, updated.Header().Get("ETag"), updated.Body.String())
	}
}

func TestFileAPISetMembershipRequiresSetRevision(t *testing.T) {
	loader := newFakeLoader()
	handler := testHandler(t, loader)
	path := apiPrefix + "/sets/" + loader.set.ID + "/files/" + loader.file.ID
	requestWithoutETag := httptest.NewRequest(http.MethodPut, path, nil)
	requestWithoutETag.Header.Set("Authorization", "Bearer test-core-key")
	missingTag := httptest.NewRecorder()
	handler.ServeHTTP(missingTag, requestWithoutETag)
	if missingTag.Code != http.StatusPreconditionRequired || loader.setAddCalls != 0 {
		t.Fatalf("missing set revision: status=%d addCalls=%d", missingTag.Code, loader.setAddCalls)
	}
	valid := httptest.NewRequest(http.MethodPut, path, nil)
	valid.Header.Set("Authorization", "Bearer test-core-key")
	valid.Header.Set("If-Match", `"`+loader.set.ID+`-r4"`)
	added := httptest.NewRecorder()
	handler.ServeHTTP(added, valid)
	if added.Code != http.StatusOK || loader.setAddCalls != 1 || loader.lastSetRevision != 4 || loader.lastFileID != loader.file.ID {
		t.Fatalf("set membership add: status=%d calls=%d set revision=%d file=%s body=%s", added.Code, loader.setAddCalls, loader.lastSetRevision, loader.lastFileID, added.Body.String())
	}
}

func TestFileAPIListsFilesWithinAnAuthorizedSet(t *testing.T) {
	loader := newFakeLoader()
	handler := testHandler(t, loader)
	path := apiPrefix + "/sets/" + loader.set.ID + "/files?group=assets&q=logo&cursor=opaque&limit=6"
	response := request(handler, http.MethodGet, path, "", true)
	if response.Code != http.StatusOK || loader.setFileListCalls != 1 || loader.lastSetID != loader.set.ID ||
		loader.lastList != (ListQuery{Group: "assets", Query: "logo", Cursor: "opaque", Limit: 6}) {
		t.Fatalf("set file listing: status=%d calls=%d set=%s query=%+v body=%s", response.Code, loader.setFileListCalls, loader.lastSetID, loader.lastList, response.Body.String())
	}
	invalidID := request(handler, http.MethodGet, apiPrefix+"/sets/not-a-uuid/files", "", true)
	if invalidID.Code != http.StatusNotFound || loader.setFileListCalls != 1 {
		t.Fatalf("invalid set id reached Loader: status=%d calls=%d body=%s", invalidID.Code, loader.setFileListCalls, invalidID.Body.String())
	}
}

func TestFileAPIRemovesFileFromASetWithItsCurrentRevision(t *testing.T) {
	loader := newFakeLoader()
	handler := testHandler(t, loader)
	path := apiPrefix + "/sets/" + loader.set.ID + "/files/" + loader.file.ID
	requestWithoutRevision := httptest.NewRequest(http.MethodDelete, path, nil)
	requestWithoutRevision.Header.Set("Authorization", "Bearer test-core-key")
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, requestWithoutRevision)
	if missing.Code != http.StatusPreconditionRequired || loader.setRemoveCalls != 0 {
		t.Fatalf("set membership removal without revision: status=%d calls=%d body=%s", missing.Code, loader.setRemoveCalls, missing.Body.String())
	}
	valid := httptest.NewRequest(http.MethodDelete, path, nil)
	valid.Header.Set("Authorization", "Bearer test-core-key")
	valid.Header.Set("If-Match", `"`+loader.set.ID+`-r4"`)
	removed := httptest.NewRecorder()
	handler.ServeHTTP(removed, valid)
	if removed.Code != http.StatusOK || loader.setRemoveCalls != 1 || loader.lastSetRevision != loader.set.Revision-1 || loader.lastFileID != loader.file.ID {
		t.Fatalf("set membership removal: status=%d calls=%d revision=%d file=%s body=%s", removed.Code, loader.setRemoveCalls, loader.lastSetRevision, loader.lastFileID, removed.Body.String())
	}
}

func TestFileAPIHidesFileSetsFromUnauthenticatedAndUnauthorizedSubjects(t *testing.T) {
	loader := newFakeLoader()
	handler := testHandler(t, loader)
	unauthenticated := request(handler, http.MethodGet, apiPrefix+"/sets/"+loader.set.ID, "", false)
	if unauthenticated.Code != http.StatusUnauthorized || loader.setGetCalls != 0 {
		t.Fatalf("unauthenticated set read: status=%d calls=%d", unauthenticated.Code, loader.setGetCalls)
	}
	loader.forbidden = true
	denied := request(handler, http.MethodGet, apiPrefix+"/sets/"+loader.set.ID, "", true)
	if denied.Code != http.StatusNotFound || strings.Contains(denied.Body.String(), loader.set.ID) {
		t.Fatalf("unauthorized set disclosure: status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestFileAPIRejectsNULInFileSetSearchBeforeCallingLoader(t *testing.T) {
	loader := newFakeLoader()
	handler := testHandler(t, loader)
	requestWithNUL := httptest.NewRequest(http.MethodGet, apiPrefix+"/sets?q=%00", nil)
	requestWithNUL.Header.Set("Authorization", "Bearer test-core-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, requestWithNUL)
	if response.Code != http.StatusBadRequest || loader.setListCalls != 0 {
		t.Fatalf("NUL search query was not rejected at the HTTP boundary: status=%d loaderCalls=%d body=%s", response.Code, loader.setListCalls, response.Body.String())
	}
}

func TestFileAPITreatsSearchCursorAsOpaqueValue(t *testing.T) {
	loader := newFakeLoader()
	handler := testHandler(t, loader)
	response := request(handler, http.MethodGet, apiPrefix+"?q=asset&cursor=opaque", "", true)
	if response.Code != http.StatusOK || loader.lastList.Cursor != "opaque" {
		t.Fatalf("opaque cursor was not forwarded: status=%d query=%+v body=%s", response.Code, loader.lastList, response.Body.String())
	}
}

func TestFileAPIRemovesAFileGroupWithItsCurrentRevision(t *testing.T) {
	loader := newFakeLoader()
	handler := testHandler(t, loader)
	path := apiPrefix + "/objects/" + loader.file.ID + "/groups/assets"
	requestWithoutRevision := httptest.NewRequest(http.MethodDelete, path, nil)
	requestWithoutRevision.Header.Set("Authorization", "Bearer test-core-key")
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, requestWithoutRevision)
	if missing.Code != http.StatusPreconditionRequired || loader.removeGroupCalls != 0 {
		t.Fatalf("group removal without revision: status=%d calls=%d", missing.Code, loader.removeGroupCalls)
	}
	valid := httptest.NewRequest(http.MethodDelete, path, nil)
	valid.Header.Set("Authorization", "Bearer test-core-key")
	valid.Header.Set("If-Match", `"`+loader.file.ID+`-r1"`)
	removed := httptest.NewRecorder()
	handler.ServeHTTP(removed, valid)
	if removed.Code != http.StatusOK || loader.removeGroupCalls != 1 || loader.lastGroup != "assets" || loader.lastRevision != 1 {
		t.Fatalf("group removal: status=%d calls=%d group=%q revision=%d body=%s", removed.Code, loader.removeGroupCalls, loader.lastGroup, loader.lastRevision, removed.Body.String())
	}
}
