package repository_test

import (
	"testing"

	"github.com/Zubrium/cdn/engines/website/repository"
)

func TestPageKeysUseGlobalScopeAndGeneratedIdentity(t *testing.T) {
	first := repository.PageKey("33333333-3333-4333-8333-333333333333")
	if first.Namespace != repository.SnapshotNamespace || first.ScopeType != "global" || first.ScopeID != "global" || first.RecordKey != "page/33333333-3333-4333-8333-333333333333" {
		t.Fatalf("page key = %#v", first)
	}
}

func TestTemplateAndIdempotencyKeysUseGlobalScopeAndOpaqueRecords(t *testing.T) {
	template := repository.PageTemplateKey("44444444-4444-4444-8444-444444444444")
	receipt := repository.PageTemplateCreateReceiptKey("client-generated-idempotency-key")
	if template.Namespace != repository.TemplateNamespace || template.ScopeType != "global" || template.ScopeID != "global" || template.RecordKey != "template/44444444-4444-4444-8444-444444444444" {
		t.Fatalf("template key = %#v", template)
	}
	if receipt.Namespace != repository.TemplateNamespace || receipt.ScopeType != "global" || receipt.ScopeID != "global" || receipt.RecordKey == "" {
		t.Fatalf("template receipt key = %#v", receipt)
	}
}
