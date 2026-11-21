package runtime_test

import "testing"

// TestWebsiteRuntimeAcceptance owns the executable assertions referenced by
// the Website scenario ledger for the static-lifecycle runtime layer. Scenarios
// that require live Composition, Router CAS features stay open in
// tests/qa/website-scenarios.json and are not claimed here.
func TestWebsiteRuntimeAcceptance(t *testing.T) {
	t.Run("WEB-023", func(t *testing.T) {
		web023ABIAndManifestValidationAreStrict(t)
	})
	t.Run("WEB-024", func(t *testing.T) {
		web024RuntimePreflightRejectsUnsafePackages(t)
	})
	t.Run("WEB-025", func(t *testing.T) {
		web025DependencyLockIsDeterministicAndFailsClosed(t)
	})
	t.Run("WEB-026", func(t *testing.T) {
		web026ArtifactValidationIsFailClosed(t)
	})
	t.Run("WEB-027", func(t *testing.T) {
		web027LifecyclePhasesAndIsolationAreEnforced(t)
	})
	t.Run("WEB-028", func(t *testing.T) {
		web028PublicationKeepsIndependentPerTargetResults(t)
	})
	t.Run("WEB-029", func(t *testing.T) {
		web029SupervisedExecutorIsFailClosed(t)
	})
}
