package router

import (
	"io"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type publicStreamCarrier struct {
	io.Reader
	ref contracts.StreamRef
}

func (carrier publicStreamCarrier) StreamReference() contracts.StreamRef { return carrier.ref }

func TestBoundedOperationBodyKeepsStreamReference(t *testing.T) {
	body := &boundedOperationBody{source: publicStreamCarrier{Reader: strings.NewReader("hello"), ref: "stream-1"}}
	ref, ok := contracts.PublicStreamReference(body)
	if !ok || ref != "stream-1" {
		t.Fatalf("stream reference = %q, ok = %v", ref, ok)
	}
}
