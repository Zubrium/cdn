package contracts

import "errors"

var ErrInvalidPublicationRequest = errors.New("invalid publication fan-out request")

// PublicationPublishRequest is the process-local trigger consumed by the
// trusted composition root. It carries no route, page, snapshot, artifact or
// operation identity; all of those remain server-owned.
type PublicationPublishRequest struct {
	Locale                     string `json:"locale"`
	ContentPublicationRevision int64  `json:"content_publication_revision"`
	SystemModelRevision        int64  `json:"system_model_revision,omitempty"`
	IdempotencyKey             string `json:"idempotency_key"`
}

type PublicationTargetResult struct {
	RouteID                string `json:"route_id"`
	Locale                 string `json:"locale"`
	State                  string `json:"state"`
	BuildID                string `json:"build_id,omitempty"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id,omitempty"`
	ErrorCode              string `json:"error_code,omitempty"`
}

type PublicationPublishResponse struct {
	RunID   string                    `json:"run_id"`
	State   string                    `json:"state"`
	Targets []PublicationTargetResult `json:"targets"`
}

func PublicationPublishOperation() Operation[PublicationPublishRequest, PublicationPublishResponse] {
	return Operation[PublicationPublishRequest, PublicationPublishResponse]{
		Key: Key{ID: "main.publication.publish", Version: 1},
		Validate: func(request PublicationPublishRequest) error {
			if request.Locale == "" || len(request.Locale) > 64 || request.ContentPublicationRevision < 0 ||
				request.SystemModelRevision < 0 || len(request.IdempotencyKey) < 16 || len(request.IdempotencyKey) > 128 {
				return ErrInvalidPublicationRequest
			}
			return nil
		},
	}
}
