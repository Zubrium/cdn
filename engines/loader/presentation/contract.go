// Package presentation defines Loader Engine's unified in-process contract.
package presentation

import (
	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"
)

type Contract interface {
	API
	platform.Module
}

// Descriptor registers Loader independently from the HTTP File API adapter.
type Descriptor struct{}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "upload.create", Action: access.Upload, ResourceType: "file"},
		{Name: "upload.read", Action: access.Read, ResourceType: "upload-session"},
		{Name: "upload.abort", Action: access.Write, ResourceType: "upload-session"},
		{Name: "file.list", Action: access.Read, ResourceType: "file"},
		{Name: "file.read", Action: access.Read, ResourceType: "file"},
		{Name: "file.write", Action: access.Write, ResourceType: "file"},
		{Name: "file.delete", Action: access.Delete, ResourceType: "file"},
		{Name: "file-set.manage", Action: access.Write, ResourceType: "file-set"},
	}
}

var _ platform.Module = Descriptor{}
