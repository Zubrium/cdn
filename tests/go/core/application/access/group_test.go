package access

import "testing"

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
