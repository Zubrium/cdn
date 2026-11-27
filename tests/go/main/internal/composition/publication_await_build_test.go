//go:build full && !devfixtures && !router

package composition

import (
	"context"
	"errors"
	"testing"
	"time"

	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

func TestPublicationAwaitBuildRetriesDurableStateConflicts(t *testing.T) {
	calls := 0
	getBuild := func(context.Context, string) (websiteapi.BuildJob, error) {
		calls++
		if calls < 3 {
			return websiteapi.BuildJob{}, websiteapi.ErrConflict
		}
		return websiteapi.BuildJob{State: "ready"}, nil
	}

	job, err := awaitBuildStatus(context.Background(), getBuild, "11111111-1111-4111-8111-111111111111", time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("awaitBuildStatus: %v", err)
	}
	if job.State != "ready" {
		t.Fatalf("state = %q, want ready", job.State)
	}
	if calls != 3 {
		t.Fatalf("getBuild calls = %d, want 3", calls)
	}
}

func TestPublicationAwaitBuildPropagatesNonConflictErrors(t *testing.T) {
	getBuild := func(context.Context, string) (websiteapi.BuildJob, error) {
		return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
	}

	_, err := awaitBuildStatus(context.Background(), getBuild, "11111111-1111-4111-8111-111111111111", time.Second, time.Millisecond)
	if !errors.Is(err, websiteapi.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}
