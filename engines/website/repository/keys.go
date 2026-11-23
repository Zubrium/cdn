// Package repository defines Website-owned engine-data keys. Persistence is
// performed only through the scoped Core engine-data facade.
package repository

import (
	"crypto/sha256"
	"encoding/hex"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"strings"
)

const (
	// SnapshotNamespace also stores page identities so page creation can
	// atomically persist the page and its ready root snapshot in one Core batch.
	SnapshotNamespace = "website/snapshots"
	TemplateNamespace = "website/page-templates"
	BuildNamespace    = "website/builds"
	MediaNamespace    = "website/media"
	ImportNamespace   = "features/imports"
	FeatureCatalogNS  = "features/catalog"
	ImportScopeID     = "import"
	CatalogScopeID    = "global"
)

// BuildScopeID is shared by a job and its idempotency receipts so their
// creation can be committed in one scoped Core batch.
const (
	BuildScopeID = "build"
	MediaScopeID = "media"
)

func PageKey(pageID string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: SnapshotNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "page/" + strings.ToLower(pageID)}
}

func PageCreateReceiptKey(idempotencyKey string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: SnapshotNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "page-idempotency/" + digestKey(idempotencyKey)}
}

func PageTemplateKey(templateID string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: TemplateNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "template/" + strings.ToLower(templateID)}
}

func PageTemplateCreateReceiptKey(idempotencyKey string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: TemplateNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "idempotency/" + digestKey(idempotencyKey)}
}

func BuildKey(id string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: BuildNamespace, ScopeType: "job", ScopeID: BuildScopeID,
		RecordKey: "build/" + strings.ToLower(id)}
}

func BuildIdempotencyKey(key string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: BuildNamespace, ScopeType: "job", ScopeID: BuildScopeID,
		RecordKey: "idempotency/" + digestKey(key)}
}

func MediaJobKey(jobID string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: MediaNamespace, ScopeType: "job", ScopeID: MediaScopeID,
		RecordKey: "job/" + strings.ToLower(jobID)}
}

func MediaIdempotencyKey(key string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: MediaNamespace, ScopeType: "job", ScopeID: MediaScopeID,
		RecordKey: "idempotency/" + digestKey(key)}
}

func FeatureImportKey(jobID string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: ImportNamespace, ScopeType: "job", ScopeID: ImportScopeID,
		RecordKey: "job/" + strings.ToLower(jobID)}
}

func FeatureImportReceiptKey(idempotencyKey string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: ImportNamespace, ScopeType: "job", ScopeID: ImportScopeID,
		RecordKey: "idempotency/" + digestKey(idempotencyKey)}
}

func FeatureVersionKey(featureID, version string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: FeatureCatalogNS, ScopeType: "global", ScopeID: CatalogScopeID,
		RecordKey: "feature/" + strings.ToLower(featureID) + "/" + strings.ToLower(version)}
}

func digestKey(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
