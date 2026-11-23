package application_test

import "testing"

// TestContentScenario runs the executable Content acceptance scenarios registered
// in tests/qa/content-scenarios.json. Each subtest delegates to an existing
// substantive contract test instead of asserting only the presence of code.
func TestContentScenario(t *testing.T) {
	t.Run("CONTENT-001", func(t *testing.T) {
		TestLocaleConfigServiceValidatesAndDelegates(t)
	})
	t.Run("CONTENT-002", func(t *testing.T) {
		TestSaveReplaysGeneratedIDsAndRejectsSameKeyWithDifferentPayload(t)
	})
	t.Run("CONTENT-003", func(t *testing.T) {
		TestPublishCreatesImmutableHistoryAndRestoreOnlyChangesDraft(t)
	})
	t.Run("CONTENT-004", func(t *testing.T) {
		TestCopySnapshotContentCopiesDraftAndHistoryAndReplaysReceipt(t)
		TestCopySnapshotContentRejectsStaleSourceBeforePublishingTargetWorkspace(t)
	})
	t.Run("CONTENT-005", func(t *testing.T) {
		t.Skip("CONTENT-005 requires the disposable PostgreSQL/S3 and live Router environment declared in tests/qa/content-scenarios.json")
	})
}
