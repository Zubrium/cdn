//go:build website_acceptance
// +build website_acceptance

package repository_test

import (
	"strings"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/website/repository"
)

func TestWebsiteAcceptancePersistenceKeys(t *testing.T) {
	const pageID = "ABCDEF12-3456-4ABC-8DEF-1234567890AB"
	const idempotencyKey = "caller-visible-idempotency-key"

	page := repository.PageKey("site-a", pageID)
	pageOtherSite := repository.PageKey("site-b", pageID)
	receipt := repository.PageCreateReceiptKey("site-a", idempotencyKey)
	template := repository.PageTemplateKey("site-a", pageID)
	templateReceipt := repository.PageTemplateCreateReceiptKey("site-a", idempotencyKey)

	if page.Namespace != repository.SnapshotNamespace || page.ScopeType != "site" || page.ScopeID != "site-a" || page.RecordKey != "page/abcdef12-3456-4abc-8def-1234567890ab" {
		t.Fatalf("page key = %#v", page)
	}
	if page == pageOtherSite || page.ScopeID == pageOtherSite.ScopeID {
		t.Fatalf("same page identity in a distinct site must remain isolated: first=%#v second=%#v", page, pageOtherSite)
	}
	for _, key := range []core.ModuleDataKey{receipt, templateReceipt} {
		if key.ScopeType != "site" || key.ScopeID != "site-a" || strings.Contains(key.RecordKey, idempotencyKey) || len(key.RecordKey) != len(strings.Split(key.RecordKey, "/")[0])+1+64 {
			t.Fatalf("idempotency receipt must be scoped and opaque: %#v", key)
		}
	}
	if receipt.Namespace != repository.SnapshotNamespace || !strings.HasPrefix(receipt.RecordKey, "page-idempotency/") {
		t.Fatalf("page creation receipt = %#v", receipt)
	}
	if template.Namespace != repository.TemplateNamespace || template.RecordKey != "template/abcdef12-3456-4abc-8def-1234567890ab" || templateReceipt.Namespace != repository.TemplateNamespace || !strings.HasPrefix(templateReceipt.RecordKey, "idempotency/") {
		t.Fatalf("template records must stay outside page snapshot namespace: template=%#v receipt=%#v", template, templateReceipt)
	}
}
