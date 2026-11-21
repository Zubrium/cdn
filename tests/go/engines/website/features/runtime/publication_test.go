package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

type recordingActivator struct {
	failFor map[string]error
	calls   []string
	next    int64
}

func (activator *recordingActivator) Activate(_ context.Context, target runtime.Target, _ runtime.Artifact) (int64, error) {
	activator.calls = append(activator.calls, target.RouteID)
	if err := activator.failFor[target.RouteID]; err != nil {
		return 0, err
	}
	activator.next++
	return activator.next, nil
}

func TestPublicationActivatesTargetsIndependentlyAndRetriesOnlyFailures(t *testing.T) {
	artifact := runtime.Artifact{ManifestFileID: "manifest-1", InputDigest: "digest-1", Locale: "de-DE"}
	targets := []runtime.Target{
		{RouteID: "route-a", Locale: "de-DE", Revision: 1, InputDigest: "digest-1", ActiveArtifact: "old-a"},
		{RouteID: "route-b", Locale: "de-DE", Revision: 2, InputDigest: "digest-1", ActiveArtifact: "old-b"},
		{RouteID: "route-stale", Locale: "de-DE", Revision: 3, InputDigest: "old-digest", ActiveArtifact: "old-c"},
		{RouteID: "route-other", Locale: "en-US", Revision: 4, InputDigest: "digest-1", ActiveArtifact: "old-d"},
	}
	activator := &recordingActivator{failFor: map[string]error{"route-b": runtime.ErrCASConflict}}
	result, err := runtime.Publish(context.Background(), targets, artifact, activator)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outcomes) != 4 {
		t.Fatalf("outcomes=%+v", result.Outcomes)
	}
	if result.Outcomes[0].State != "activated" || result.Outcomes[0].Revision != 1 ||
		result.Outcomes[1].State != "failed" || result.Outcomes[1].ActiveArtifact != "old-b" ||
		result.Outcomes[2].State != "stale" || result.Outcomes[3].State != "skipped_locale" {
		t.Fatalf("unexpected per-target results: %+v", result.Outcomes)
	}

	activator.failFor = nil
	retry, err := runtime.RetryFailed(context.Background(), result, targets, artifact, activator)
	if err != nil {
		t.Fatal(err)
	}
	byRoute := make(map[string]runtime.TargetOutcome, len(retry.Outcomes))
	for _, outcome := range retry.Outcomes {
		byRoute[outcome.RouteID] = outcome
	}
	if len(retry.Outcomes) != 4 || byRoute["route-a"].State != "activated" || byRoute["route-a"].Revision != 1 ||
		byRoute["route-b"].State != "activated" || byRoute["route-stale"].State != "stale" ||
		byRoute["route-other"].State != "skipped_locale" {
		t.Fatalf("retry changed successful targets or missed the failed one: %+v", retry.Outcomes)
	}
	if len(activator.calls) != 3 || activator.calls[0] != "route-a" || activator.calls[1] != "route-b" || activator.calls[2] != "route-b" {
		t.Fatalf("unexpected activator calls: %v", activator.calls)
	}
}

func web028PublicationKeepsIndependentPerTargetResults(t *testing.T) {
	TestPublicationActivatesTargetsIndependentlyAndRetriesOnlyFailures(t)
	TestPublicationRejectsInvalidArtifact(t)
}

func TestPublicationRejectsInvalidArtifact(t *testing.T) {
	_, err := runtime.Publish(context.Background(), nil, runtime.Artifact{}, &recordingActivator{})
	if !errors.Is(err, runtime.ErrPublication) {
		t.Fatalf("err=%v", err)
	}
}
