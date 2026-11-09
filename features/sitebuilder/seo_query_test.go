package sitebuilder

import (
	"testing"

	publicruntime "websiteengine/pkg/runtime"
)

func TestMetadataWithQueryRobots(t *testing.T) {
	for _, test := range []struct {
		name     string
		robots   string
		hasQuery bool
		want     string
	}{
		{name: "indexable without query", robots: "index,follow", want: "index,follow"},
		{name: "query variant", robots: "index,follow", hasQuery: true, want: "noindex,follow"},
		{name: "preview remains nofollow", robots: "noindex,nofollow", hasQuery: true, want: "noindex,nofollow"},
		{name: "explicit page noindex remains nofollow", robots: "noindex,nofollow", want: "noindex,nofollow"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := metadataWithQueryRobots(publicruntime.HeadMetadata{Robots: test.robots}, test.hasQuery)
			if got.Robots != test.want {
				t.Fatalf("robots directive = %q, want %q", got.Robots, test.want)
			}
		})
	}
}
