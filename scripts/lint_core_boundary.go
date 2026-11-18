// lint_core_boundary enforces the one-way dependency boundary around Core.
// Core is a reusable library: it may depend on its own packages and external
// libraries, while workspace consumers may import only core/presentation.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type module struct {
	dir  string
	path string
}

func main() {
	root, err := repositoryRoot()
	if err != nil {
		fail(err)
	}

	modules, err := workspaceModules(root)
	if err != nil {
		fail(err)
	}
	localModules, err := repositoryModules(root)
	if err != nil {
		fail(err)
	}
	core, ok := moduleAt(modules, filepath.Join(root, "core"))
	if !ok {
		fail(fmt.Errorf("core is not listed as a module in go.work"))
	}

	var violations []string
	coreMod, err := os.ReadFile(filepath.Join(core.dir, "go.mod"))
	if err != nil {
		fail(err)
	}
	for _, candidate := range localModules {
		if candidate.dir == core.dir {
			continue
		}
		if strings.Contains(string(coreMod), candidate.path) {
			violations = append(violations, fmt.Sprintf("core/go.mod references repository module %q", candidate.path))
		}
	}

	for _, owner := range localModules {
		err := filepath.WalkDir(owner.dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != owner.dir && (entry.Name() == ".git" || entry.Name() == "vendor") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}

			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return fmt.Errorf("parse %s: %w", relative(root, path), err)
			}
			for _, imported := range parsed.Imports {
				importPath, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return fmt.Errorf("decode import in %s: %w", relative(root, path), err)
				}

				if owner.dir == core.dir {
					for _, dependency := range localModules {
						if dependency.dir != core.dir && hasModulePrefix(importPath, dependency.path) {
							violations = append(violations, fmt.Sprintf("%s imports repository module %q", relative(root, path), importPath))
						}
					}
					continue
				}

				if hasModulePrefix(importPath, core.path) && importPath != core.path+"/presentation" {
					violations = append(violations, fmt.Sprintf("%s imports private Core package %q; use %s/presentation", relative(root, path), importPath, core.path))
				}
			}
			return nil
		})
		if err != nil {
			fail(err)
		}
	}

	if len(violations) > 0 {
		fmt.Fprintln(os.Stderr, "Core architecture boundary violations:")
		for _, violation := range violations {
			fmt.Fprintf(os.Stderr, "  - %s\n", violation)
		}
		os.Exit(1)
	}
	fmt.Printf("Core boundary OK: Core has no repository-local module dependencies; checked Core API imports across %d repository-local Go modules.\n", len(localModules))
}

func repositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.work")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("cannot locate repository root containing go.work")
		}
		current = parent
	}
}

func workspaceModules(root string) ([]module, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return nil, err
	}

	var paths []string
	inUseBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if inUseBlock {
			if fields[0] == ")" {
				inUseBlock = false
				continue
			}
			paths = append(paths, strings.Trim(fields[0], "\""))
			continue
		}
		if fields[0] != "use" {
			continue
		}
		if len(fields) > 1 && fields[1] == "(" {
			inUseBlock = true
			continue
		}
		if len(fields) > 1 {
			paths = append(paths, strings.Trim(fields[1], "\""))
		}
	}
	if inUseBlock {
		return nil, fmt.Errorf("unterminated use block in go.work")
	}

	modules := make([]module, 0, len(paths))
	seen := make(map[string]bool)
	for _, path := range paths {
		dir := path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		dir, err = filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		if seen[dir] {
			return nil, fmt.Errorf("duplicate workspace module directory %s", relative(root, dir))
		}
		seen[dir] = true

		candidate, err := readModule(dir, root)
		if err != nil {
			return nil, err
		}
		modules = append(modules, candidate)
	}
	return modules, nil
}

func repositoryModules(root string) ([]module, error) {
	var modules []module
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == ".git" || entry.Name() == ".codegraph" || entry.Name() == "node_modules" || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "go.mod" {
			return nil
		}
		candidate, err := readModule(filepath.Dir(path), root)
		if err != nil {
			return err
		}
		modules = append(modules, candidate)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return modules, nil
}

func readModule(dir, root string) (module, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return module{}, err
	}
	goMod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return module{}, fmt.Errorf("read go.mod for %s: %w", relative(root, dir), err)
	}
	for _, line := range strings.Split(string(goMod), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return module{dir: dir, path: strings.Trim(fields[1], "\"")}, nil
		}
	}
	return module{}, fmt.Errorf("missing module directive in %s/go.mod", relative(root, dir))
}

func moduleAt(modules []module, dir string) (module, bool) {
	dir, _ = filepath.Abs(dir)
	for _, candidate := range modules {
		if candidate.dir == dir {
			return candidate, true
		}
	}
	return module{}, false
}

func hasModulePrefix(importPath, modulePath string) bool {
	return importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")
}

func relative(root, path string) string {
	value, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(value)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "lint-core-boundary:", err)
	os.Exit(2)
}
