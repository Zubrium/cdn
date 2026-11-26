package contracts

import (
	"errors"
	"fmt"
	"testing"
)

func TestRetirementRouteErrorCodeDoesNotExposeDependencyErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{fmt.Errorf("wrapped: %w", ErrRetirementNotFound), "not_found"},
		{ErrRetirementConflict, "revision_conflict"},
		{ErrRetirementUnavailable, "unavailable"},
		{errors.New("password=secret host=internal"), "route_failed"},
		{errors.New("/private/customer/path: failed"), "route_failed"},
	} {
		if got := RetirementRouteErrorCode(tc.err); got != tc.want {
			t.Errorf("error code = %q, want %q", got, tc.want)
		}
	}
}
