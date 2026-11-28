package contracts

// These operations are internal to the compiled publication and retirement
// workflows. The receiver selects the service identity from startup config.
type OrchestrationObjectRequest struct {
	ID string `json:"id"`
}

type PublicationInputRequest struct {
	SnapshotID      string `json:"snapshot_id"`
	Locale          string `json:"locale"`
	ContentRevision int64  `json:"content_revision"`
}

type PublicationInputResponse struct {
	Digest              string `json:"digest"`
	SystemModelRevision int64  `json:"system_model_revision"`
}

type PublicationWebsiteBuildRequest struct {
	PublicationInputRequest
	IdempotencyKey string `json:"idempotency_key"`
}

type PublicationRouteCASRequest struct {
	ReplaceLocaleTargetRequest
	IdempotencyKey string `json:"idempotency_key"`
}

type OrchestrationRouteRequest struct {
	RouteID string `json:"route_id"`
	Locale  string `json:"locale,omitempty"`
}

type OrchestrationRouteState struct {
	ID       string                    `json:"id"`
	State    string                    `json:"state"`
	Revision int64                     `json:"revision"`
	Slot     *OrchestrationLocaleState `json:"slot,omitempty"`
}

type OrchestrationLocaleState struct {
	Locale                 string `json:"locale"`
	Revision               int64  `json:"revision"`
	ArtifactManifestFileID string `json:"artifact_manifest_file_id"`
}

type RetirementWebsiteTransitionRequest struct {
	PageID           string `json:"page_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	LifecycleState   string `json:"lifecycle_state"`
	RetirementJobID  string `json:"retirement_job_id"`
	IdempotencyKey   string `json:"idempotency_key"`
}

func PublicationInputOperation() Operation[PublicationInputRequest, PublicationInputResponse] {
	return Operation[PublicationInputRequest, PublicationInputResponse]{Key: Key{ID: "website.publication.input", Version: 1}}
}

func PublicationWebsiteBuildStartOperation() Operation[PublicationWebsiteBuildRequest, AdminWebsiteBuildJob] {
	return Operation[PublicationWebsiteBuildRequest, AdminWebsiteBuildJob]{Key: Key{ID: "website.publication.build.start", Version: 1}}
}

func PublicationWebsiteBuildReadOperation() Operation[OrchestrationObjectRequest, AdminWebsiteBuildJob] {
	return Operation[OrchestrationObjectRequest, AdminWebsiteBuildJob]{Key: Key{ID: "website.publication.build.read", Version: 1}}
}

func OrchestrationRouteReadOperation() Operation[OrchestrationRouteRequest, OrchestrationRouteState] {
	return Operation[OrchestrationRouteRequest, OrchestrationRouteState]{Key: Key{ID: "router.orchestration.route.read", Version: 1}}
}

func PublicationRouteCASOperation() Operation[PublicationRouteCASRequest, OrchestrationRouteState] {
	return Operation[PublicationRouteCASRequest, OrchestrationRouteState]{Key: Key{ID: "router.publication.locale.cas", Version: 1}}
}

func RetirementRouteDeleteOperation() Operation[DeleteRouteRequest, struct{}] {
	return Operation[DeleteRouteRequest, struct{}]{Key: Key{ID: "router.retirement.route.delete", Version: 1}}
}

func RetirementWebsiteReadOperation() Operation[OrchestrationObjectRequest, AdminWebsitePage] {
	return Operation[OrchestrationObjectRequest, AdminWebsitePage]{Key: Key{ID: "website.retirement.page.read", Version: 1}}
}

func RetirementWebsiteTransitionOperation() Operation[RetirementWebsiteTransitionRequest, AdminWebsitePage] {
	return Operation[RetirementWebsiteTransitionRequest, AdminWebsitePage]{Key: Key{ID: "website.retirement.page.transition", Version: 1}}
}
