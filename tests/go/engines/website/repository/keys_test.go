package repository_test

import (
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/website/repository"
)

func TestPageKeysAreScopedToSiteAndUseGeneratedIdentity(t *testing.T) {
	first := repository.PageKey("site-a", "33333333-3333-4333-8333-333333333333")
	second := repository.PageKey("site-b", "33333333-3333-4333-8333-333333333333")
	if first.Namespace != repository.SnapshotNamespace || first.ScopeType != "site" || first.ScopeID != "site-a" || first.RecordKey != "page/33333333-3333-4333-8333-333333333333" {
		t.Fatalf("page key = %#v", first)
	}
	if first == second {
		t.Fatal("identical page IDs in distinct sites must remain separately scoped")
	}
}

func TestTemplateAndIdempotencyKeysAreSiteScopedAndOpaque(t *testing.T) {
	template := repository.PageTemplateKey("site-a", "44444444-4444-4444-8444-444444444444")
	receipt := repository.PageTemplateCreateReceiptKey("site-a", "client-generated-idempotency-key")
	if template.Namespace != repository.TemplateNamespace || template.ScopeType != "site" || template.ScopeID != "site-a" || template.RecordKey != "template/44444444-4444-4444-8444-444444444444" {
		t.Fatalf("template key = %#v", template)
	}
	if receipt.Namespace != repository.TemplateNamespace || receipt.ScopeID != "site-a" || len(receipt.RecordKey) != len("idempotency/")+64 {
		t.Fatalf("template receipt key = %#v", receipt)
	}
	if receipt == (core.ModuleDataKey{}) || receipt.RecordKey == "idempotency/client-generated-idempotency-key" {
		t.Fatalf("receipt must not expose the caller's idempotency key: %#v", receipt)
	}
}
