package sitebuilder

import (
	"net/http/httptest"
	"testing"
)

func TestContentSnapshotListFailsClosedWithoutConfiguredPagination(t *testing.T) {
	response := httptest.NewRecorder()
	NewSnapshotsHandler(nil, nil, nil, 1).ContentSnapshots(response, httptest.NewRequest("GET", "/?limit=1", nil))
	if response.Code != 500 {
		t.Fatalf("unconfigured collection status = %d, want 500", response.Code)
	}
}

func TestSnapshotPaginationRejectsInvalidConfiguration(t *testing.T) {
	handler := NewSnapshotsHandler(nil, nil, nil, 1)
	if err := handler.SetPagination(0, 200); err == nil {
		t.Fatal("zero default page size accepted")
	}
	if err := handler.SetPagination(50, 49); err == nil {
		t.Fatal("maximum below default page size accepted")
	}
}
