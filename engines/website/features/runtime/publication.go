package runtime

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrCASConflict = errors.New("router target revision conflict")
	ErrTargetStale = errors.New("router target input is stale")
	ErrTargetGone  = errors.New("router target is not registered")
	ErrActivation  = errors.New("router target activation failed")
)

// Artifact is the immutable output of one render. Its digest binds the render
// inputs; a target built from another digest is stale and must not be switched.
type Artifact struct {
	ManifestFileID string
	InputDigest    string
	Locale         string
}

// Target is one registered Router route/locale target. The activator performs
// the single-target CAS; this package never chooses routes or URLs.
type Target struct {
	RouteID        string
	Locale         string
	Revision       int64
	InputDigest    string
	ActiveArtifact string
}

type TargetActivator interface {
	Activate(context.Context, Target, Artifact) (int64, error)
}

type TargetOutcome struct {
	RouteID        string
	Locale         string
	State          string
	Revision       int64
	ActiveArtifact string
	Error          string
}

type PublicationResult struct {
	ArtifactID string
	Outcomes   []TargetOutcome
}

// Publish activates every matching target independently. A failed or stale
// target keeps its previous artifact; successful targets are never rolled back.
func Publish(ctx context.Context, targets []Target, artifact Artifact, activator TargetActivator) (PublicationResult, error) {
	if artifact.ManifestFileID == "" || artifact.InputDigest == "" || activator == nil {
		return PublicationResult{}, fmt.Errorf("%w: artifact and activator are required", ErrPublication)
	}
	result := PublicationResult{ArtifactID: artifact.ManifestFileID}
	for _, target := range targets {
		outcome := TargetOutcome{RouteID: target.RouteID, Locale: target.Locale,
			State: "failed", Revision: target.Revision, ActiveArtifact: target.ActiveArtifact}
		switch {
		case target.Locale != artifact.Locale:
			outcome.State = "skipped_locale"
		case target.InputDigest != artifact.InputDigest:
			outcome.State = "stale"
		default:
			revision, err := activator.Activate(ctx, target, artifact)
			switch {
			case err == nil:
				outcome.State = "activated"
				outcome.Revision = revision
				outcome.ActiveArtifact = artifact.ManifestFileID
			case errors.Is(err, ErrCASConflict):
				outcome.Error = ErrCASConflict.Error()
			case errors.Is(err, ErrTargetGone):
				outcome.Error = ErrTargetGone.Error()
			default:
				outcome.Error = ErrActivation.Error()
			}
		}
		result.Outcomes = append(result.Outcomes, outcome)
	}
	return result, nil
}

var ErrPublication = errors.New("invalid publication request")

// RetryFailed re-attempts only failed targets. It is safe to call repeatedly:
// an already activated target is not contacted again.
func RetryFailed(ctx context.Context, previous PublicationResult, targets []Target, artifact Artifact, activator TargetActivator) (PublicationResult, error) {
	failed := make(map[string]struct{})
	for _, outcome := range previous.Outcomes {
		if outcome.State == "failed" {
			failed[outcome.RouteID+"\x00"+outcome.Locale] = struct{}{}
		}
	}
	if len(failed) == 0 {
		return previous, nil
	}
	selected := make([]Target, 0, len(targets))
	for _, target := range targets {
		if _, ok := failed[target.RouteID+"\x00"+target.Locale]; ok {
			selected = append(selected, target)
		}
	}
	retry, err := Publish(ctx, selected, artifact, activator)
	if err != nil {
		return previous, err
	}
	merged := PublicationResult{ArtifactID: previous.ArtifactID}
	for _, outcome := range previous.Outcomes {
		if _, ok := failed[outcome.RouteID+"\x00"+outcome.Locale]; !ok {
			merged.Outcomes = append(merged.Outcomes, outcome)
		}
	}
	merged.Outcomes = append(merged.Outcomes, retry.Outcomes...)
	return merged, nil
}
