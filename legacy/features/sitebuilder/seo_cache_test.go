package sitebuilder

import (
	"testing"

	"github.com/google/uuid"
	"websiteengine/pkg/environment"
)

func TestSEOIndexCacheKeyTracksBranchSnapshotContentAndIndexingPolicy(t *testing.T) {
	env := environment.Environment("9e61d4f4-a693-4af4-82af-5f77347e8eaa")
	record := SnapshotRecord{ID: uuid.MustParse("45909b2a-f8dd-4e6a-bb18-903c50e56249"), ContentRevision: 4, Digest: "structural-v1"}
	metadata := DefaultSiteMetadata()
	metadata.CanonicalDomain = "https://shop.example"
	payload := SnapshotPayload{Metadata: &metadata}
	base, err := seoIndexCacheKey(env, record, payload, true)
	if err != nil {
		t.Fatal(err)
	}
	changedRevision := record
	changedRevision.ContentRevision++
	changedBranch, _ := seoIndexCacheKey(environment.Environment("7e5b9ca8-1f64-4779-bff6-df10102f3022"), record, payload, true)
	changedRevisionKey, _ := seoIndexCacheKey(env, changedRevision, payload, true)
	changedDigest := record
	changedDigest.Digest = "structural-v2"
	changedDigestKey, _ := seoIndexCacheKey(env, changedDigest, payload, true)
	changedPolicy, _ := seoIndexCacheKey(env, record, payload, false)
	metadata.NoIndex = true
	changedMetadata, _ := seoIndexCacheKey(env, record, payload, true)
	for name, candidate := range map[string]string{
		"branch": changedBranch, "content revision": changedRevisionKey, "structural digest": changedDigestKey,
		"branch indexing policy": changedPolicy, "site indexing metadata": changedMetadata,
	} {
		if candidate == base {
			t.Errorf("changing %s did not invalidate the SEO index identity", name)
		}
	}
}
