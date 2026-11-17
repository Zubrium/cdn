package coordinator

import "testing"

func TestProfileChecksDependenciesAndDuplicates(t *testing.T) {
	registry := Registry{
		"core":        {ID: "core"},
		"coordinator": {ID: "coordinator", Requires: []string{"core"}},
		"loader":      {ID: "loader", Requires: []string{"core", "coordinator"}},
		"files":       {ID: "files", Requires: []string{"loader", "coordinator"}},
	}
	for _, tc := range []struct {
		name     string
		selected []string
		valid    bool
	}{
		{"complete", []string{"core", "coordinator", "loader", "files"}, true},
		{"missing coordinator", []string{"core", "loader", "files"}, false},
		{"missing dependency", []string{"core", "coordinator", "files"}, false},
		{"unknown", []string{"core", "coordinator", "other"}, false},
		{"duplicate", []string{"core", "coordinator", "coordinator"}, false},
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
	r := Registry{"core": {ID: "core"}, "coordinator": {ID: "coordinator", Requires: []string{"core"}}, "a": {ID: "a", Requires: []string{"b"}}, "b": {ID: "b", Requires: []string{"a"}}}
	if err := r.Validate([]string{"core", "coordinator", "a", "b"}); err == nil {
		t.Fatal("dependency cycle accepted")
	}
}
