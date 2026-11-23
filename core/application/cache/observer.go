package cache

import "context"

// Label is one low-cardinality metric label. Core owns the vocabulary; an
// outer adapter maps it to the deployed observability backend. This keeps the
// shared pkg module out of Core's dependency boundary.
type Label struct {
	Key   string
	Value string
}

// StringLabel builds a string-valued cache metric label.
func StringLabel(key, value string) Label { return Label{Key: key, Value: value} }

// Observer is the minimal metrics sink required by Core's bounded caches.
// Implementations live outside Core so Core never imports a repository module.
type Observer interface {
	IncCounter(ctx context.Context, name string, value float64, labels ...Label)
	SetGauge(ctx context.Context, name string, value float64, labels ...Label)
	ObserveHistogram(ctx context.Context, name string, value float64, labels ...Label)
}
