package application

import "testing"

// TestLoaderScenario runs the executable Loader acceptance scenarios registered
// in tests/qa/loader-scenarios.json. Each subtest delegates to existing
// substantive contract tests instead of asserting only the presence of code.
func TestLoaderScenario(t *testing.T) {
	t.Run("LOADER-001", func(t *testing.T) {
		TestBeginUploadIdempotencyIsPrincipalScopedAndDoesNotCollapseKeys(t)
		TestValidUploadRejectsChangedInputFingerprint(t)
		TestIdempotencyTTLTracksSessionDuringAndAfterCommit(t)
	})
	t.Run("LOADER-002", func(t *testing.T) {
		TestUploadPartsCommitAndCommitRetryReturnSameFile(t)
		TestPendingCoreCommitRetainsExpiredSessionAndPartData(t)
		TestExpiredCommitRecoveryPromotesReadyFileBeforeCleanup(t)
		TestExpiredCommitRecoveryMarksFailedCoreRequestAborted(t)
	})
	t.Run("LOADER-003", func(t *testing.T) {
		TestAbortSchedulesDurableStagingCleanup(t)
		TestExpiredUploadCannotBeExtendedByPartWrite(t)
		TestCleanupCursorRotatesPastPendingCoreCommits(t)
		TestCleanupRefusesMismatchedJobIdentity(t)
		TestCleanupToleratesJobRemovedByAnotherInstance(t)
	})
	t.Run("LOADER-004", func(t *testing.T) {
		TestCommitFailsFastWithinConfiguredConcurrencyLimit(t)
		TestConcurrencyAdmissionScenario(t)
		TestCommitRechecksCurrentGroupAccess(t)
		TestSessionCacheEnforcesByteBudgetAndRevision(t)
		TestAuthenticateSessionUsesCoreSessionAndRejectsInactivePrincipal(t)
	})
	t.Run("LOADER-005", func(t *testing.T) {
		t.Skip("LOADER-005 requires the disposable Core/PostgreSQL/S3 and live Router environment declared in tests/qa/loader-scenarios.json")
	})
}
