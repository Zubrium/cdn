package access

import "testing"

func TestEffectiveLevelIsHighestAssignmentOnAnyActiveFileGroup(t *testing.T) {
	assignments := []GroupAssignment{
		{Group: "features", Level: LevelRead, Active: true},
		{Group: "content", Level: LevelWrite, Active: true},
		{Group: "disabled", Level: LevelWrite, Active: false},
		{Group: "invalid", Level: Level(3), Active: true},
	}
	for _, tc := range []struct {
		name   string
		groups []string
		want   Level
	}{
		{"read through one group", []string{"features"}, LevelRead},
		{"write through another group", []string{"features", "content"}, LevelWrite},
		{"disabled group grants nothing", []string{"disabled"}, LevelNone},
		{"invalid level grants nothing", []string{"invalid"}, LevelNone},
		{"unknown group grants nothing", []string{"unknown"}, LevelNone},
		{"no file groups grants nothing", nil, LevelNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveLevel(assignments, tc.groups); got != tc.want {
				t.Fatalf("EffectiveLevel(%v) = %d, want %d", tc.groups, got, tc.want)
			}
		})
	}
}

func TestAccessGroupKeyValidationMatchesConfiguredKeyContract(t *testing.T) {
	for _, value := range []string{"", "Content", "1asset", "asset/path", "asset group", "asset.", "_asset", string(make([]byte, 65))} {
		if ValidGroupKey(value) {
			t.Errorf("accepted invalid group key %q", value)
		}
	}
	for _, value := range []string{"a", "content", "feature-2", "model_data"} {
		if !ValidGroupKey(value) {
			t.Errorf("rejected valid group key %q", value)
		}
	}
}

func TestDelegationRequiresManagementAndWriteLevel(t *testing.T) {
	for _, tc := range []struct {
		name            string
		active, manages bool
		own, requested  Level
		want            bool
	}{
		{"grant level 0", true, true, LevelWrite, LevelNone, true},
		{"grant level 1", true, true, LevelWrite, LevelRead, true},
		{"grant level 2", true, true, LevelWrite, LevelWrite, true},
		{"cannot exceed level 2", true, true, LevelWrite, Level(3), false},
		{"read access cannot delegate", true, true, LevelRead, LevelRead, false},
		{"no access cannot delegate", true, true, LevelNone, LevelRead, false},
		{"management right alone cannot delegate", true, false, LevelWrite, LevelRead, false},
		{"inactive user cannot delegate", false, true, LevelWrite, LevelRead, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanDelegate(tc.active, tc.manages, tc.own, tc.requested); got != tc.want {
				t.Fatalf("CanDelegate() = %v, want %v", got, tc.want)
			}
		})
	}
}
