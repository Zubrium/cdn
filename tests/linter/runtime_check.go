package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	contractsImport = "github.com/Zubrium/cdn/pkg/contracts"
	busImport       = "github.com/Zubrium/cdn/main/internal/bus"
)

func checkRuntimeBusIsolation(root string, modules []module) []string {
	var violations []string
	for _, owner := range modules {
		if owner.dir == filepath.Join(root, "main") || owner.dir == filepath.Join(root, "pkg") {
			continue
		}
		err := filepath.WalkDir(owner.dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != owner.dir && (entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "vendor") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", relative(root, path), err)
			}
			aliases := make(map[string]bool)
			for _, imported := range file.Imports {
				value, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return fmt.Errorf("decode import in %s: %w", relative(root, path), err)
				}
				if value == busImport {
					violations = append(violations, fmt.Sprintf("%s imports composition-private bus package", relative(root, path)))
				}
				if value != contractsImport {
					continue
				}
				if imported.Name != nil && imported.Name.Name != "_" && imported.Name.Name != "." {
					aliases[imported.Name.Name] = true
				} else {
					aliases["contracts"] = true
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.CompositeLit:
					if !isContractsEnvelope(value.Type, aliases) {
						return true
					}
					violations = append(violations, fmt.Sprintf("%s constructs a bus envelope outside main; call a typed operation instead", relative(root, path)))
				case *ast.CallExpr:
					selector, ok := value.Fun.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "Dispatch" {
						return true
					}
					if identifier, ok := selector.X.(*ast.Ident); ok && aliases[identifier.Name] {
						violations = append(violations, fmt.Sprintf("%s dispatches a raw operation outside main", relative(root, path)))
						return true
					}
					violations = append(violations, fmt.Sprintf("%s calls a raw Dispatch method outside main", relative(root, path)))
				}
				return true
			})
			return nil
		})
		if err != nil {
			fail(err)
		}
	}
	return violations
}

func isContractsEnvelope(expression ast.Expr, aliases map[string]bool) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Envelope" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && aliases[identifier.Name]
}
