//go:build devfixtures && !full && !router

package composition

import "testing"

func TestDevelopmentFixturesProfileStaticAllowlistIsExact(t *testing.T) {
	assertCompiledDispatcherAllowlist(t, BuildPlan())
}
