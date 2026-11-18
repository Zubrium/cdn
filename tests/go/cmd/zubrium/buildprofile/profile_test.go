package buildprofile

import "testing"

func TestProfileChecksDependenciesAndDuplicates(t *testing.T) {
	registry := Registry{
		"core":   {ID: "core"},
		"loader": {ID: "loader", Requires: []string{"core"}},
		"files":  {ID: "files", Requires: []string{"loader"}},
	}
	for _, tc := range []struct {
		name     string
		selected []string
		valid    bool
	}{
		{"complete", []string{"core", "loader", "files"}, true},
		{"missing dependency", []string{"core", "files"}, false},
		{"unknown", []string{"core", "other"}, false},
		{"duplicate", []string{"core", "core"}, false},
		{"missing core", []string{"loader"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := registry.Validate(tc.selected)
			if (err == nil) != tc.valid {
				t.Fatalf("Validate(%v) = %v", tc.selected, err)
			}
		})
	}
}

func TestProfileRejectsCycles(t *testing.T) {
	r := Registry{"core": {ID: "core"}, "a": {ID: "a", Requires: []string{"b"}}, "b": {ID: "b", Requires: []string{"a"}}}
	if err := r.Validate([]string{"core", "a", "b"}); err == nil {
		t.Fatal("dependency cycle accepted")
	}
}
