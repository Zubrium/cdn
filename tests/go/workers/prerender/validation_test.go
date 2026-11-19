package prerender

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
)

func planRequest(plan []byte, inputDigest string) Request {
	sum := sha256.Sum256(plan)
	return Request{JobID: "job-1", IdempotencyKey: "stable-1", Plan: plan, PlanDigest: hex.EncodeToString(sum[:]), InputDigest: inputDigest}
}

func testLimits() Limits {
	return Limits{MaxPlanBytes: 4096, MaxArtifacts: 4, MaxArtifactBytes: 1024, MaxTotalBytes: 2048}
}

func TestValidatePlanRequiresVersionAndMatchingDigests(t *testing.T) {
	input := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plan := []byte(`{"version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{"routes":[]}}`)
	request := planRequest(plan, input)
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
	input := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, plan := range [][]byte{
		[]byte(`{"version":"render-plan/v2","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{}}`),
		[]byte(`{"version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{},"command":"/bin/sh"}`),
		[]byte(`{"version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{}} {}`),
	} {
		if err := ValidatePlan(planRequest(plan, input), testLimits()); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("plan %s: got %v", plan, err)
		}
	}
	request := planRequest([]byte(`{"version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{}}`), input)
	limits := testLimits()
	limits.MaxPlanBytes = 8
	if err := ValidatePlan(request, limits); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("oversized plan: %v", err)
	}
	if err := ValidatePlan(request, Limits{}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatal("missing limits accepted")
	}
}

func TestValidateResultBoundsAndBindsArtifactSetToInputs(t *testing.T) {
	input := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	checksum := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	result := Result{Manifest: Artifact{FileID: "manifest-file", Checksum: checksum, ContentType: "application/json", Size: 256}, InputDigest: input, Artifacts: []Artifact{{FileID: "index-html", Checksum: checksum, ContentType: "text/html", Size: 768}, {FileID: "asset-css", Checksum: checksum, ContentType: "text/css", Size: 768}}}
	limits := testLimits()
	if err := ValidateResult(limits, input, result); err != nil {
		t.Fatal(err)
	}
	if err := ValidateResult(limits, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", result); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("stale digest: %v", err)
	}
	result.Artifacts[1].FileID = "index-html"
	if err := ValidateResult(limits, input, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("duplicate file reference accepted")
	}
	result.Artifacts[1].FileID, result.Artifacts[1].Size = "asset-css", 1025
	if err := ValidateResult(limits, input, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("oversized artifact accepted")
	}
	result.Artifacts[1].FileID, result.Artifacts[1].Size = "../outside", 768
	if err := ValidateResult(limits, input, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("unsafe artifact file ID accepted")
	}
	result.Artifacts[1].FileID, result.Artifacts[1].ContentType = "asset-css", "image/svg+xml"
	if err := ValidateResult(limits, input, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("active SVG artifact content type accepted")
	}
	result.Artifacts[1].ContentType = "text/css"
	result.Manifest.ContentType = "text/html"
	if err := ValidateResult(limits, input, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("non-JSON artifact manifest accepted")
	}
}

func TestValidateResultRequiresExactlyOneHTMLDocument(t *testing.T) {
	input := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	checksum := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	manifest := Artifact{FileID: "manifest-file", Checksum: checksum, ContentType: "application/json", Size: 32}
	css := Artifact{FileID: "asset-css", Checksum: checksum, ContentType: "text/css", Size: 32}
	result := Result{Manifest: manifest, InputDigest: input, Artifacts: []Artifact{css}}
	if err := ValidateResult(testLimits(), input, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("artifact without HTML document accepted: %v", err)
	}
	result.Artifacts = []Artifact{
		{FileID: "page-one", Checksum: checksum, ContentType: "text/html; charset=utf-8", Size: 32},
		{FileID: "page-two", Checksum: checksum, ContentType: "text/html", Size: 32},
	}
	if err := ValidateResult(testLimits(), input, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("artifact with multiple HTML documents accepted: %v", err)
	}
}

func TestValidatePlanRejectsAmbiguousEnvelope(t *testing.T) {
	input := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, plan := range [][]byte{
		[]byte(`{"version":"render-plan/v1","version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{}}`),
		[]byte(`{"version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{"x":1,"x":2}}`),
		[]byte(`{"Version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{}}`),
		[]byte(`{"version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":null}`),
		[]byte(`{"version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{},"extra":true}`),
	} {
		if err := ValidatePlan(planRequest(plan, input), testLimits()); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("ambiguous envelope %s: got %v", plan, err)
		}
	}
}

func TestFailClosedWorkerValidatesWithoutProducingArtifacts(t *testing.T) {
	input := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plan := []byte(`{"version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{}}`)
	worker := FailClosedWorker{Limits: testLimits()}
	result, err := worker.Render(context.Background(), planRequest(plan, input))
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

func TestTaskHandlerUsesRegisteredIdentityAndFailsClosed(t *testing.T) {
	input := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plan := []byte(`{"version":"render-plan/v1","input_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","payload":{}}`)
	request := planRequest(plan, input)
	request.JobID = "00000000-0000-0000-0000-000000000000"
	request.IdempotencyKey = "stable-1"
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	job := core.CoreJob{ComponentID: DataModuleID, OperationID: OperationID, ContractVersion: 1, IdempotencyKey: "stable-1", Payload: payload}
	worker := FailClosedWorker{Limits: testLimits()}
	if _, err := worker.TaskHandler(context.Background(), core.CoreJob{ComponentID: "other"}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("unregistered operation accepted: %v", err)
	}
	if outcome, err := worker.TaskHandler(context.Background(), job); !errors.Is(err, ErrRenderUnavailable) || len(outcome.Result) != 0 {
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
