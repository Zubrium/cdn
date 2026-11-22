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
	BuildNamespace    = "website/builds"
	ImportNamespace   = "features/imports"
	FeatureCatalogNS  = "features/catalog"
	ImportScopeID     = "import"
	CatalogScopeID    = "global"
)

// BuildScopeID is shared by a job and its idempotency receipts so their
// creation can be committed in one scoped Core batch.
const BuildScopeID = "build"

func PageKey(pageID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: SnapshotNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "page/" + strings.ToLower(pageID)}
}

func PageCreateReceiptKey(idempotencyKey string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: SnapshotNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "page-idempotency/" + digestKey(idempotencyKey)}
}

func PageTemplateKey(templateID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: TemplateNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "template/" + strings.ToLower(templateID)}
}

func PageTemplateCreateReceiptKey(idempotencyKey string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: TemplateNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "idempotency/" + digestKey(idempotencyKey)}
}

func BuildKey(id string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: BuildNamespace, ScopeType: "job", ScopeID: BuildScopeID,
		RecordKey: "build/" + strings.ToLower(id)}
}

func BuildIdempotencyKey(key string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: BuildNamespace, ScopeType: "job", ScopeID: BuildScopeID,
		RecordKey: "idempotency/" + digestKey(key)}
}

func FeatureImportKey(jobID string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: ImportNamespace, ScopeType: "job", ScopeID: ImportScopeID,
		RecordKey: "job/" + strings.ToLower(jobID)}
}

func FeatureImportReceiptKey(idempotencyKey string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: ImportNamespace, ScopeType: "job", ScopeID: ImportScopeID,
		RecordKey: "idempotency/" + digestKey(idempotencyKey)}
}

func FeatureVersionKey(featureID, version string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: FeatureCatalogNS, ScopeType: "global", ScopeID: CatalogScopeID,
		RecordKey: "feature/" + strings.ToLower(featureID) + "/" + strings.ToLower(version)}
}

func digestKey(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
