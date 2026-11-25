//go:build router && !full && !devfixtures

package composition

import "testing"

func TestRouterProfileStaticAllowlistIsExact(t *testing.T) {
	assertCompiledDispatcherAllowlist(t, BuildPlan())
}
