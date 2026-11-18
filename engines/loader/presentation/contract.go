// Package presentation defines Loader Engine's unified in-process contract.
package presentation

import (
	platform "github.com/Zubrium/cdn/core/presentation"
)

type Contract interface {
	API
	platform.Module
}

// Descriptor registers Loader independently from the HTTP File API adapter.
type Descriptor struct{}

func (Descriptor) ComponentDescriptor() platform.ComponentDescriptor {
	return platform.ComponentDescriptor{ID: "engine.loader", Kind: "engine", Version: 1}
}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "upload.create", Action: platform.AccessActionUpload, ResourceType: "file"},
		{Name: "upload.read", Action: platform.AccessActionRead, ResourceType: "upload-session"},
		{Name: "upload.abort", Action: platform.AccessActionWrite, ResourceType: "upload-session"},
		{Name: "file.list", Action: platform.AccessActionRead, ResourceType: "file"},
		{Name: "file.read", Action: platform.AccessActionRead, ResourceType: "file"},
		{Name: "file.write", Action: platform.AccessActionWrite, ResourceType: "file"},
		{Name: "file.delete", Action: platform.AccessActionDelete, ResourceType: "file"},
		{Name: "file-set.manage", Action: platform.AccessActionWrite, ResourceType: "file-set"},
	}
}

var _ platform.Module = Descriptor{}
