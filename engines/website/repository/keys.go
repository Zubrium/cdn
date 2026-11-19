// Package repository defines Website-owned engine-data keys. Persistence is
// performed only through the scoped Core engine-data facade.
package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
)

const (
	// SnapshotNamespace also stores page identities so page creation can
	// atomically persist the page and its ready root snapshot in one Core batch.
	SnapshotNamespace = "website/snapshots"
	TemplateNamespace = "website/page-templates"
)

func PageKey(siteID, pageID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: SnapshotNamespace, ScopeType: "site", ScopeID: siteID, RecordKey: "page/" + strings.ToLower(pageID)}
}

func PageCreateReceiptKey(siteID, idempotencyKey string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: SnapshotNamespace, ScopeType: "site", ScopeID: siteID, RecordKey: "page-idempotency/" + digestKey(idempotencyKey)}
}

func PageTemplateKey(siteID, templateID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: TemplateNamespace, ScopeType: "site", ScopeID: siteID, RecordKey: "template/" + strings.ToLower(templateID)}
}

func PageTemplateCreateReceiptKey(siteID, idempotencyKey string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: TemplateNamespace, ScopeType: "site", ScopeID: siteID, RecordKey: "idempotency/" + digestKey(idempotencyKey)}
}

func digestKey(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
