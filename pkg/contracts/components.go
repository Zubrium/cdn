package contracts

// ComponentDescriptor is profile metadata owned by main. Core does not inspect
// component kinds, dependencies, operations, or access-group declarations.
type ComponentDescriptor struct {
	ID           string               `json:"id"`
	Kind         string               `json:"kind"`
	Version      int                  `json:"version"`
	Dependencies []string             `json:"dependencies,omitempty"`
	Operations   []ComponentOperation `json:"operations,omitempty"`
	AccessGroups []string             `json:"access_groups,omitempty"`
}

type ComponentOperation struct {
	ID              string `json:"id"`
	ContractVersion int    `json:"contract_version"`
}

type ComponentDescriptorProvider interface {
	ComponentDescriptor() ComponentDescriptor
}
