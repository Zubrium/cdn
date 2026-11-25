package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// strictBoundaryBaseline records debt that still has to be migrated. Entries
// are removed as each wave lands; the final linter freeze requires an empty
// baseline.
var strictBoundaryBaseline = map[string]bool{}

const (
	corePresentationImportPath = "github.com/Zubrium/cdn/core/presentation"
	adminPresentationPrefix    = "github.com/Zubrium/cdn/presentation/admin/"
	engineImportPrefix         = "github.com/Zubrium/cdn/engines/"
)

var coreRuntimeMethods = map[string]bool{
	"Identity":           true,
	"Sessions":           true,
	"Files":              true,
	"NewWorkflowClient":  true,
	"NewWorkflowControl": true,
	"CoordinatorReady":   true,
}

func checkStrictBoundaries(root string, modules []module) []string {
	var violations []string
	violations = append(violations, checkCompositionRuntimeHandles(root)...)
	violations = append(violations, checkPresentationFacadeImports(root, modules)...)
	violations = append(violations, checkPublicRouterAdminAuth(root)...)
	violations = append(violations, checkGlobalPresentationState(root)...)
	violations = append(violations, checkRouterFilesystemStaging(root)...)
	violations = append(violations, checkLocalModuleCycles(modules)...)
	return violations
}

func strictViolation(violations []string, key, message string) []string {
	if strictBoundaryBaseline[key] {
		return violations
	}
	return append(violations, message+" (baseline key: "+key+")")
}

func walkGoFiles(base string, skipTests bool, visit func(path string, file *ast.File, fset *token.FileSet, imports map[string]string) error) error {
	return filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != base && (entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || skipTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		imports := make(map[string]string)
		for _, imported := range file.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return fmt.Errorf("decode import in %s: %w", path, err)
			}
			alias := pathBase(value)
			if imported.Name != nil {
				alias = imported.Name.Name
			}
			imports[alias] = value
		}
		return visit(path, file, fset, imports)
	})
}

func pathBase(value string) string {
	index := strings.LastIndex(value, "/")
	if index < 0 {
		return value
	}
	return value[index+1:]
}

func expressionText(fset *token.FileSet, expression ast.Expr) string {
	var buffer bytes.Buffer
	if err := printer.Fprint(&buffer, fset, expression); err != nil {
		return ""
	}
	return buffer.String()
}

func aliasSet(imports map[string]string, predicate func(string) bool) map[string]bool {
	result := make(map[string]bool)
	for alias, importPath := range imports {
		if predicate(importPath) {
			result[alias] = true
		}
	}
	return result
}

func checkCompositionRuntimeHandles(root string) []string {
	// Composition is the only place allowed to hold Core runtime handles while
	// adapting them to DataBus clients. Domain packages under main/internal
	// must not retain those handles themselves.
	base := filepath.Join(root, "main", "internal")
	domainDirs := map[string]bool{
		filepath.Join(base, "publication"):   true,
		filepath.Join(base, "retirement"):    true,
		filepath.Join(base, "contentbridge"): true,
	}
	var violations []string
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if !domainDirs[path] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		imports := make(map[string]string)
		for _, imported := range file.Imports {
			value, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			alias := pathBase(value)
			if imported.Name != nil {
				alias = imported.Name.Name
			}
			imports[alias] = value
		}
		relativeFile := relative(root, path)
		coreAliases := aliasSet(imports, func(value string) bool { return value == corePresentationImportPath })
		engineAliases := aliasSet(imports, func(value string) bool { return strings.HasPrefix(value, engineImportPrefix) })
		directRuntime := false

		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range structType.Fields.List {
					selector, ok := unwrapPointerSelector(field.Type)
					if !ok {
						continue
					}
					if isAliasedSelector(selector, coreAliases, "Runtime") || engineAliases[selector.X.(*ast.Ident).Name] {
						directRuntime = true
					}
				}
			}
		}

		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || !coreRuntimeMethods[selector.Sel.Name] {
				return true
			}
			if strings.Contains(expressionText(fset, selector.X), "runtime") {
				directRuntime = true
			}
			return true
		})

		if directRuntime {
			violations = strictViolation(violations,
				"composition-runtime|"+relativeFile,
				relativeFile+" keeps a direct Core runtime or engine service handle; route cross-module calls through DataBus")
		}
		return nil
	})
	if err != nil {
		return append(violations, fmt.Sprintf("scan domain runtime handles: %v", err))
	}
	return violations
}

func unwrapPointerSelector(expression ast.Expr) (*ast.SelectorExpr, bool) {
	star, ok := expression.(*ast.StarExpr)
	if ok {
		expression = star.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	_, ok = selector.X.(*ast.Ident)
	return selector, ok
}

func isAliasedSelector(selector *ast.SelectorExpr, aliases map[string]bool, name string) bool {
	identifier, ok := selector.X.(*ast.Ident)
	return ok && aliases[identifier.Name] && selector.Sel.Name == name
}

func checkPresentationFacadeImports(root string, modules []module) []string {
	var violations []string
	for _, owner := range modules {
		if !isPresentationModule(owner.path) {
			continue
		}
		err := walkGoFiles(owner.dir, true, func(path string, file *ast.File, fset *token.FileSet, imports map[string]string) error {
			for _, importPath := range imports {
				if !strings.HasPrefix(importPath, engineImportPrefix) {
					continue
				}
				relativeFile := relative(root, path)
				violations = strictViolation(violations,
					"presentation-engine-import|"+relativeFile+"|"+importPath,
					relativeFile+" imports engine package "+importPath+"; Presentation must call engines through DataBus")
			}
			return nil
		})
		if err != nil {
			violations = append(violations, fmt.Sprintf("scan presentation engine imports in %s: %v", relative(root, owner.dir), err))
		}
	}
	return violations
}

func checkPublicRouterAdminAuth(root string) []string {
	base := filepath.Join(root, "presentation", "router")
	var violations []string
	err := walkGoFiles(base, true, func(path string, file *ast.File, fset *token.FileSet, imports map[string]string) error {
		for _, importPath := range imports {
			if !strings.HasPrefix(importPath, adminPresentationPrefix) {
				continue
			}
			relativeFile := relative(root, path)
			violations = strictViolation(violations,
				"public-router-admin-auth|"+relativeFile,
				relativeFile+" imports an Admin Presentation module; public Router must not depend on Admin")
		}
		return nil
	})
	if err != nil {
		return append(violations, fmt.Sprintf("scan public Router Admin imports: %v", err))
	}
	return violations
}

func checkGlobalPresentationState(root string) []string {
	var violations []string
	for _, owner := range []string{"presentation/admin", "presentation/router"} {
		err := walkGoFiles(filepath.Join(root, owner), true, func(path string, file *ast.File, fset *token.FileSet, imports map[string]string) error {
			for _, declaration := range file.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || general.Tok != token.VAR {
					continue
				}
				for _, specification := range general.Specs {
					valueSpec, ok := specification.(*ast.ValueSpec)
					if !ok {
						continue
					}
					if !astContainsMutex(valueSpec.Type) {
						continue
					}
					relativeFile := relative(root, path)
					violations = strictViolation(violations,
						"presentation-global-state|"+relativeFile,
						relativeFile+" declares process-global synchronized state; keep state instance-scoped")
				}
			}
			return nil
		})
		if err != nil {
			violations = append(violations, fmt.Sprintf("scan Presentation global state in %s: %v", owner, err))
		}
	}
	return violations
}

func astContainsMutex(expression ast.Expr) bool {
	if expression == nil {
		return false
	}
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && (selector.Sel.Name == "Mutex" || selector.Sel.Name == "RWMutex") {
			found = true
		}
		return !found
	})
	return found
}

func checkRouterFilesystemStaging(root string) []string {
	base := filepath.Join(root, "presentation", "router", "publicupload")
	var violations []string
	err := walkGoFiles(base, true, func(path string, file *ast.File, fset *token.FileSet, imports map[string]string) error {
		if imports["os"] == "os" || imports["filepath"] == "path/filepath" {
			relativeFile := relative(root, path)
			violations = strictViolation(violations,
				"router-fs-staging|"+relativeFile,
				relativeFile+" uses the filesystem for upload staging; Loader must stage bytes through Core")
		}
		return nil
	})
	if err != nil {
		return append(violations, fmt.Sprintf("scan Router staging: %v", err))
	}
	return violations
}

func checkLocalModuleCycles(modules []module) []string {
	requires := make(map[string]map[string]bool, len(modules))
	local := make(map[string]bool, len(modules))
	for _, owner := range modules {
		local[owner.path] = true
	}
	for _, owner := range modules {
		data, err := os.ReadFile(filepath.Join(owner.dir, "go.mod"))
		if err != nil {
			return []string{fmt.Sprintf("read %s/go.mod for cycle check: %v", relative(".", owner.dir), err)}
		}
		paths := goModRequirePaths(data)
		requires[owner.path] = make(map[string]bool)
		for dependency := range paths {
			if local[dependency] {
				requires[owner.path][dependency] = true
			}
		}
	}

	starts := make([]string, 0, len(modules))
	for _, owner := range modules {
		starts = append(starts, owner.path)
	}
	sort.Strings(starts)

	state := make(map[string]int, len(starts))
	stack := make([]string, 0, len(starts))
	seen := make(map[string]bool)
	var violations []string
	var visit func(string)
	visit = func(node string) {
		state[node] = 1
		stack = append(stack, node)
		dependencies := make([]string, 0, len(requires[node]))
		for dependency := range requires[node] {
			dependencies = append(dependencies, dependency)
		}
		sort.Strings(dependencies)
		for _, dependency := range dependencies {
			switch state[dependency] {
			case 0:
				visit(dependency)
			case 1:
				index := -1
				for position, candidate := range stack {
					if candidate == dependency {
						index = position
						break
					}
				}
				if index < 0 {
					continue
				}
				cycle := append([]string{}, stack[index:]...)
				key := canonicalCycle(cycle)
				if !seen[key] {
					seen[key] = true
					violations = strictViolation(violations,
						"module-cycle|"+key,
						"Go module dependency cycle: "+strings.Join(append(append([]string{}, cycle...), dependency), " -> "))
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = 2
	}
	for _, start := range starts {
		if state[start] == 0 {
			visit(start)
		}
	}
	return violations
}

func canonicalCycle(path []string) string {
	best := append([]string{}, path...)
	for index := 1; index < len(path); index++ {
		candidate := append(append([]string{}, path[index:]...), path[:index]...)
		if strings.Join(candidate, "\x00") < strings.Join(best, "\x00") {
			best = candidate
		}
	}
	return strings.Join(best, "|")
}
