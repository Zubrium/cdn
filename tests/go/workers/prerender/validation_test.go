package prerender

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
	prerendercontract "github.com/Zubrium/cdn/pkg/contracts/prerender"
	"github.com/google/uuid"
)

const web020InputDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func trustedInput() prerendercontract.TrustedInput {
	return prerendercontract.TrustedInput{
		Version: prerendercontract.TrustedInputVersion, SnapshotID: "22222222-2222-4222-8222-222222222222",
		SnapshotRevision: 1, SnapshotDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SystemModelRevision: 1, SystemModelDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Locale: "en-US", LocaleCatalogRevision: 1, ContentPublicationRevision: 1,
		LocaleSelectionDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		FeaturePackageDigest:  "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		DependencyLockDigest:  "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		RuntimeABI:            prerendercontract.RuntimeABI, RuntimeAPIVersion: prerendercontract.RuntimeAPIVersion,
	}
}

func validPlanBytes(t *testing.T) []byte {
	t.Helper()
	input := trustedInput()
	digest, err := prerendercontract.InputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prerendercontract.MarshalPlan(prerendercontract.Plan{
		Version: prerendercontract.PlanVersion, InputDigest: digest, Input: input,
		Page: prerendercontract.PagePlan{Title: "en-US", BodyHTML: "<html><body>test</body></html>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func planRequest(plan []byte, inputDigest string) Request {
	sum := sha256.Sum256(plan)
	return Request{TaskID: "task-1", IdempotencyKey: "stable-1", Plan: plan, PlanDigest: hex.EncodeToString(sum[:]), InputDigest: inputDigest}
}

func testLimits() Limits {
	return Limits{MaxPlanBytes: 8192, MaxArtifacts: 4, MaxArtifactBytes: 1024, MaxTotalBytes: 2048}
}

func TestValidatePlanRequiresVersionAndMatchingDigests(t *testing.T) {
	plan := validPlanBytes(t)
	var envelope struct {
		InputDigest string `json:"input_digest"`
	}
	if err := json.Unmarshal(plan, &envelope); err != nil {
		t.Fatal(err)
	}
	request := planRequest(plan, envelope.InputDigest)
	if err := ValidatePlan(request, testLimits()); err != nil {
		t.Fatal(err)
	}
	request.PlanDigest = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if !errors.Is(ValidatePlan(request, testLimits()), ErrPlanDigestMismatch) {
		t.Fatal("tampered plan accepted")
	}
	request = planRequest(plan, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	if !errors.Is(ValidatePlan(request, testLimits()), ErrInvalidPlan) {
		t.Fatal("input digest mismatch accepted")
	}
}

func TestValidatePlanRejectsUnsupportedOrUnboundedEnvelope(t *testing.T) {
	for _, plan := range [][]byte{
		[]byte(`{"version":"render-plan/v1","input_digest":"` + web020InputDigest + `","payload":{}}`),
		append(append([]byte(nil), validPlanBytes(t)...), []byte(` {}`)...),
		[]byte(`{"version":"render-plan/v2","input_digest":"` + web020InputDigest + `","payload":{}}`),
	} {
		if err := ValidatePlan(planRequest(plan, web020InputDigest), testLimits()); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("plan %s: got %v", plan, err)
		}
	}
	limits := testLimits()
	limits.MaxPlanBytes = 8
	if err := ValidatePlan(planRequest(validPlanBytes(t), web020InputDigest), limits); !errors.Is(err, ErrInvalidPlan) {
		t.Fatal("oversized plan accepted")
	}
	if err := ValidatePlan(planRequest(validPlanBytes(t), web020InputDigest), Limits{}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatal("missing limits accepted")
	}
}

func TestValidateResultBoundsAndBindsArtifactSetToInputs(t *testing.T) {
	checksum := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	result := Result{Manifest: Artifact{FileID: "manifest-file", Checksum: checksum, ContentType: prerendercontract.ArtifactMediaType, Size: 256}, InputDigest: web020InputDigest, Artifacts: []Artifact{{FileID: "index-html", Checksum: checksum, ContentType: "text/html", Size: 768}, {FileID: "asset-css", Checksum: checksum, ContentType: "text/css", Size: 768}}}
	limits := testLimits()
	if err := ValidateResult(limits, web020InputDigest, result); err != nil {
		t.Fatal(err)
	}
	if err := ValidateResult(limits, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("stale digest accepted")
	}
	result.Artifacts[1].FileID = "index-html"
	if err := ValidateResult(limits, web020InputDigest, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("duplicate file reference accepted")
	}
	result.Artifacts[1].FileID, result.Artifacts[1].Size = "asset-css", 1025
	if err := ValidateResult(limits, web020InputDigest, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("oversized artifact accepted")
	}
	result.Artifacts[1].FileID, result.Artifacts[1].Size = "../outside", 768
	if err := ValidateResult(limits, web020InputDigest, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("unsafe artifact file ID accepted")
	}
	result.Artifacts[1].FileID, result.Artifacts[1].ContentType = "asset-css", "image/svg+xml"
	if err := ValidateResult(limits, web020InputDigest, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("active SVG artifact content type accepted")
	}
	result.Artifacts[1].ContentType = "text/css"
	result.Manifest.ContentType = "text/html"
	if err := ValidateResult(limits, web020InputDigest, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("non-manifest artifact content type accepted")
	}
}

func TestValidateResultRequiresExactlyOneHTMLDocument(t *testing.T) {
	checksum := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	manifest := Artifact{FileID: "manifest-file", Checksum: checksum, ContentType: prerendercontract.ArtifactMediaType, Size: 32}
	css := Artifact{FileID: "asset-css", Checksum: checksum, ContentType: "text/css", Size: 32}
	result := Result{Manifest: manifest, InputDigest: web020InputDigest, Artifacts: []Artifact{css}}
	if err := ValidateResult(testLimits(), web020InputDigest, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("artifact without HTML document accepted: %v", err)
	}
	result.Artifacts = []Artifact{
		{FileID: "page-one", Checksum: checksum, ContentType: "text/html; charset=utf-8", Size: 32},
		{FileID: "page-two", Checksum: checksum, ContentType: "text/html", Size: 32},
	}
	if err := ValidateResult(testLimits(), web020InputDigest, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("artifact with multiple HTML documents accepted")
	}
}

func TestValidatePlanRejectsAmbiguousEnvelope(t *testing.T) {
	valid := string(validPlanBytes(t))
	for _, plan := range []string{
		`{"version":"render-plan/v2","version":"render-plan/v2"}`,
		valid[:len(valid)-1] + `,"version":"render-plan/v2"}`,
		`{"Version":"render-plan/v2"}`,
		`{"version":"render-plan/v2","input_digest":"` + web020InputDigest + `","input":null}`,
		valid[:len(valid)-1] + `,"extra":true}`,
	} {
		if err := ValidatePlan(planRequest([]byte(plan), web020InputDigest), testLimits()); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("ambiguous envelope %s: got %v", plan, err)
		}
	}
}

func TestFailClosedWorkerValidatesWithoutProducingArtifacts(t *testing.T) {
	plan := validPlanBytes(t)
	var envelope struct {
		InputDigest string `json:"input_digest"`
	}
	if err := json.Unmarshal(plan, &envelope); err != nil {
		t.Fatal(err)
	}
	worker := FailClosedWorker{Limits: testLimits()}
	result, err := worker.Render(context.Background(), planRequest(plan, envelope.InputDigest))
	if !errors.Is(err, ErrRenderUnavailable) || result.Manifest.FileID != "" || len(result.Artifacts) != 0 {
		t.Fatalf("worker must fail closed without output: result=%+v err=%v", result, err)
	}
}

func TestFailClosedWorkerRejectsNilAndCancelledContextsBeforeValidation(t *testing.T) {
	worker := FailClosedWorker{Limits: testLimits()}
	if _, err := worker.Render(nil, Request{}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("nil context: got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := worker.Render(ctx, Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: got %v", err)
	}
}

func TestWorkerHandlerUsesRegisteredIdentityAndFailsClosed(t *testing.T) {
	plan := validPlanBytes(t)
	var envelope struct {
		InputDigest string `json:"input_digest"`
	}
	if err := json.Unmarshal(plan, &envelope); err != nil {
		t.Fatal(err)
	}
	request := planRequest(plan, envelope.InputDigest)
	request.TaskID = ""
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	task := contracts.OperationTask{TaskID: uuid.NewString(), IdempotencyKey: "stable-1", Payload: payload}
	worker := FailClosedWorker{Limits: testLimits()}
	if _, err := worker.WorkerHandler(context.Background(), contracts.OperationTask{}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatal("task without an identity was accepted")
	}
	if outcome, err := worker.WorkerHandler(context.Background(), task); !errors.Is(err, ErrRenderUnavailable) || len(outcome.Result) != 0 {
		t.Fatalf("task returned success without supervised renderer: outcome=%+v err=%v", outcome, err)
	}
	if descriptor := (Descriptor{}).ComponentDescriptor(); descriptor.ID != DataModuleID || len(descriptor.Operations) != 1 || descriptor.Operations[0].ID != OperationID || descriptor.Operations[0].ContractVersion != 1 {
		t.Fatalf("unexpected worker descriptor: %+v", descriptor)
	}
}

func TestPlanDigestDeterministic(t *testing.T) {
	plan := []byte(`{"version":"render-plan/v1"}`)
	if PlanDigest(plan) != PlanDigest(plan) || PlanDigest(plan) == PlanDigest([]byte(`{"version":"render-plan/v2"}`)) {
		t.Fatal("unexpected digest behavior")
	}
}
