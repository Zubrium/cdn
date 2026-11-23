// Package presentation defines Loader Engine's unified in-process contract.
package presentation

import (
	"github.com/Zubrium/cdn/pkg/contracts"
)

type Contract interface {
	API
}

// Descriptor registers Loader independently from the HTTP File API adapter.
type Descriptor struct{}

func (Descriptor) ComponentDescriptor() contracts.ComponentDescriptor {
	return contracts.ComponentDescriptor{ID: "engine.loader", Kind: "engine", Version: 1}
}
