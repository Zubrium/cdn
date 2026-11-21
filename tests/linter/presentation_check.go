package main

import "strings"

const enginePathMarker = "/engines/"

// Known legacy imports of engine internals from outer Presentation adapters.
// The baseline freezes the current set: any new internal-engine dependency is
// a violation until the adapter is migrated to the engine's public facade.
var presentationEngineImportBaseline = map[string]bool{
	"presentation/admin/content/handler.go|github.com/Zubrium/cdn/engines/content/domain":                         true,
	"presentation/router/delivery.go|github.com/Zubrium/cdn/engines/router/features/routes/application":          true,
	"presentation/router/delivery.go|github.com/Zubrium/cdn/engines/router/features/routes/model":                true,
	"presentation/router/publicupload/upload.go|github.com/Zubrium/cdn/engines/loader/features/files/pkg":        true,
}

func isPresentationModule(modulePath string) bool {
	return strings.Contains(modulePath, "/presentation/") && !strings.HasPrefix(modulePath, "github.com/Zubrium/cdn/core/")
}

func isInternalEngineImport(importPath string) bool {
	index := strings.LastIndex(importPath, enginePathMarker)
	if index < 0 {
		return false
	}
	parts := strings.Split(importPath[index+len(enginePathMarker):], "/")
	return len(parts) > 1 && parts[1] != "presentation"
}

func presentationImportAllowed(relativeFile, importPath string) bool {
	return presentationEngineImportBaseline[relativeFile+"|"+importPath]
}
