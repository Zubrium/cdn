package forms_test

import "testing"

// TestFormsScenario runs the executable Forms acceptance scenarios registered
// in tests/qa/forms-scenarios.json. Each subtest delegates to existing
// substantive contract tests instead of asserting only the presence of code.
func TestFormsScenario(t *testing.T) {
	t.Run("FORMS-001", func(t *testing.T) {
		TestSaveSchemaUsesGeneratedIDsAndIdempotentUniqueKey(t)
		TestSaveSchemaRejectsDuplicateKeyAndUsesCASOnUpdate(t)
		TestFormKeyIsGloballyUniqueAndReleasedOnRename(t)
	})
	t.Run("FORMS-002", func(t *testing.T) {
		TestSchemaValidationRejectsUnsupportedAndUnsafeFieldDefinitions(t)
		TestSchemaAcceptsExplicitSupportedFieldTypesAndConstraints(t)
	})
	t.Run("FORMS-003", func(t *testing.T) {
		TestPublicFormSessionValidatesAndAcceptsImmutableSubmission(t)
		TestPublicFormSessionCannotSubmitInvalidOrUnknownValues(t)
		TestCompletedUploadRetryRemainsSafeAfterItsFormWasSubmitted(t)
		TestPublicFormRouteVersionChangeRejectsExistingSession(t)
	})
	t.Run("FORMS-004", func(t *testing.T) {
		TestSchemaAndSubmissionPermissionsAreIndependent(t)
		TestListSubmissionsAppliesFormAndStateSelectors(t)
		TestSubmissionStatusUpdateUsesCAS(t)
		TestDeleteSubmissionResumesAfterPartialAttachmentCleanup(t)
	})
	t.Run("FORMS-005", func(t *testing.T) {
		t.Skip("FORMS-005 requires the disposable PostgreSQL/S3 and live Router environment declared in tests/qa/forms-scenarios.json")
	})
}
