package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type profileComponent struct {
	id       string
	requires []string
}

type profileRegistry map[string]profileComponent

func isolationProfileRegistry() profileRegistry {
	return profileRegistry{
		"core":                   {id: "core"},
		"engine.loader":          {id: "engine.loader", requires: []string{"core"}},
		"engine.website":         {id: "engine.website", requires: []string{"core"}},
		"engine.content":         {id: "engine.content", requires: []string{"core", "engine.website"}},
		"engine.forms":           {id: "engine.forms", requires: []string{"core"}},
		"engine.router":          {id: "engine.router", requires: []string{"core"}},
		"development.fixtures":   {id: "development.fixtures", requires: []string{"core", "engine.website", "engine.content", "engine.forms", "admin.website", "admin.content", "admin.forms", "admin.identity"}},
		"worker.archive":         {id: "worker.archive", requires: []string{"core"}},
		"worker.media":           {id: "worker.media", requires: []string{"core"}},
		"worker.prerender":       {id: "worker.prerender", requires: []string{"core"}},
		"admin.files":            {id: "admin.files", requires: []string{"engine.loader"}},
		"admin.website":          {id: "admin.website", requires: []string{"engine.website", "admin.files"}},
		"admin.content":          {id: "admin.content", requires: []string{"engine.content", "admin.files"}},
		"admin.forms":            {id: "admin.forms", requires: []string{"engine.forms", "admin.files"}},
		"admin.router":           {id: "admin.router", requires: []string{"engine.router"}},
		"admin.coordinator":      {id: "admin.coordinator", requires: []string{"core", "admin.files"}},
		"admin.identity":         {id: "admin.identity", requires: []string{"core", "admin.files"}},
		"admin.notifications":    {id: "admin.notifications", requires: []string{"admin.identity", "admin.files"}},
		"admin.ui":               {id: "admin.ui", requires: []string{"core"}},
		"public.router":          {id: "public.router", requires: []string{"engine.router"}},
		"public.form-session":    {id: "public.form-session", requires: []string{"engine.forms", "public.router"}},
		"public.form-submit":     {id: "public.form-submit", requires: []string{"engine.forms", "public.router"}},
		"public.form-upload":     {id: "public.form-upload", requires: []string{"engine.loader", "public.router"}},
		"public.content-runtime": {id: "public.content-runtime", requires: []string{"engine.website", "engine.content", "engine.router", "public.router"}},
	}
}

var isolationGeneratedImports = map[string]string{
	"engine.loader":          "engines/loader/presentation",
	"engine.website":         "engines/website/presentation",
	"engine.content":         "engines/content/presentation",
	"engine.forms":           "engines/forms/presentation",
	"engine.router":          "engines/router/presentation",
	"worker.archive":         "workers/archive",
	"worker.media":           "workers/media",
	"worker.prerender":       "workers/prerender",
	"admin.files":            "presentation/fileapi",
	"admin.website":          "presentation/sitesapi",
	"admin.content":          "presentation/admin/content",
	"admin.forms":            "presentation/admin/forms",
	"admin.router":           "presentation/admin/router",
	"admin.coordinator":      "presentation/admin/base/transport",
	"admin.identity":         "presentation/admin/base/transport",
	"admin.notifications":    "presentation/admin/base/transport",
	"admin.ui":               "presentation/adminui",
	"public.router":          "presentation/router",
	"public.form-upload":     "presentation/router/publicupload",
	"public.form-session":    "presentation/router/publicforms",
	"public.form-submit":     "presentation/router/publicforms",
	"public.content-runtime": "presentation/router/publiccontentruntime",
	"development.fixtures":   "main/internal/devfixtures",
}

type profileDocument struct {
	version    int
	engines    []string
	workers    []string
	devFixture bool
	admin      []string
	public     []string
}

var profilegenLine = regexp.MustCompile(`profilegen -profile (\S+) -out (\S+) -package (\S+) -tag '([^']*)'`)

func checkIsolationProfiles(root string) []string {
	var violations []string
	makefile, err := os.ReadFile(filepath.Join(root, "main", "Makefile"))
	if err != nil {
		return append(violations, fmt.Sprintf("read main/Makefile: %v", err))
	}
	matches := profilegenLine.FindAllStringSubmatch(string(makefile), -1)
	if len(matches) == 0 {
		return append(violations, "main/Makefile does not define any profilegen targets")
	}
	seenProfiles := make(map[string]string, len(matches))
	for _, match := range matches {
		profileName, outputName, packageName, buildTag := match[1], match[2], match[3], match[4]
		profilePath := filepath.Join(root, "main", profileName)
		outputPath := filepath.Join(root, "main", outputName)
		data, err := os.ReadFile(profilePath)
		if err != nil {
			violations = append(violations, fmt.Sprintf("read profile %s: %v", profileName, err))
			continue
		}
		profile, err := parseIsolationProfile(data)
		if err != nil {
			violations = append(violations, fmt.Sprintf("parse profile %s: %v", profileName, err))
			continue
		}
		plan, err := isolationBuildPlan(profile)
		if err != nil {
			violations = append(violations, fmt.Sprintf("resolve profile %s: %v", profileName, err))
			continue
		}
		generated, err := parseGeneratedProfile(outputPath)
		if err != nil {
			violations = append(violations, fmt.Sprintf("parse generated profile %s: %v", relative(root, outputPath), err))
			continue
		}
		if generated.packageName != packageName {
			violations = append(violations, fmt.Sprintf("%s declares package %q, Makefile expects %q", relative(root, outputPath), generated.packageName, packageName))
		}
		if generated.buildTag != buildTag {
			violations = append(violations, fmt.Sprintf("%s has build tag %q, Makefile expects %q", relative(root, outputPath), generated.buildTag, buildTag))
		}
		if !equalStrings(generated.plan, plan) {
			violations = append(violations, fmt.Sprintf("%s plan %v does not match profile %s plan %v", relative(root, outputPath), generated.plan, profileName, plan))
		}
		if generated.devFixtures != profile.devFixture {
			violations = append(violations, fmt.Sprintf("%s development fixtures = %t, profile %s = %t", relative(root, outputPath), generated.devFixtures, profileName, profile.devFixture))
		}
		wantImports := map[string]bool{"github.com/Zubrium/cdn/core/presentation": true}
		for _, component := range plan {
			if path := isolationGeneratedImports[component]; path != "" {
				wantImports["github.com/Zubrium/cdn/"+path] = true
			}
		}
		for imported := range generated.imports {
			if !wantImports[imported] {
				violations = append(violations, fmt.Sprintf("%s imports unselected module %q", relative(root, outputPath), imported))
			}
		}
		for imported := range wantImports {
			if !generated.imports[imported] {
				violations = append(violations, fmt.Sprintf("%s omits selected module %q", relative(root, outputPath), imported))
			}
		}
		seenProfiles[profileName] = outputName
	}
	for _, file := range mustGlob(filepath.Join(root, "main", "profiles", "*.yaml")) {
		name, err := filepath.Rel(filepath.Join(root, "main"), file)
		if err != nil {
			name = file
		}
		if _, ok := seenProfiles[filepath.ToSlash(name)]; !ok {
			violations = append(violations, fmt.Sprintf("profile %s is not compiled by main/Makefile", name))
		}
	}
	return violations
}

func parseIsolationProfile(data []byte) (profileDocument, error) {
	var profile profileDocument
	seen := make(map[string]bool)
	section := ""
	for number, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if line == "" {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if strings.HasPrefix(line, "- ") {
			value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "- ")), "\"'")
			switch section {
			case "engines":
				profile.engines = append(profile.engines, value)
			case "workers":
				profile.workers = append(profile.workers, value)
			case "apis.admin":
				profile.admin = append(profile.admin, value)
			case "apis.public":
				profile.public = append(profile.public, value)
			default:
				return profile, fmt.Errorf("line %d: list item outside a list", number+1)
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return profile, fmt.Errorf("line %d: expected key: value", number+1)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if indent == 0 {
			section = ""
			if seen[key] {
				return profile, fmt.Errorf("line %d: duplicate key %q", number+1, key)
			}
			seen[key] = true
			switch key {
			case "version":
				parsed, err := strconv.Atoi(value)
				if err != nil {
					return profile, fmt.Errorf("line %d: invalid version %q", number+1, value)
				}
				profile.version = parsed
			case "development_fixtures":
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return profile, fmt.Errorf("line %d: invalid development_fixtures %q", number+1, value)
				}
				profile.devFixture = parsed
			case "engines", "workers":
				if value != "" && value != "[]" {
					return profile, fmt.Errorf("line %d: %s must use a block list", number+1, key)
				}
				section = key
			case "apis":
				if value != "" {
					return profile, fmt.Errorf("line %d: apis must be a mapping", number+1)
				}
				section = "apis"
			default:
				return profile, fmt.Errorf("line %d: unknown profile key %q", number+1, key)
			}
			continue
		}
		if indent > 0 && (section == "apis" || strings.HasPrefix(section, "apis.")) {
			if value != "" && value != "[]" {
				return profile, fmt.Errorf("line %d: %s must use a block list", number+1, key)
			}
			switch key {
			case "admin":
				section = "apis.admin"
			case "public":
				section = "apis.public"
			default:
				return profile, fmt.Errorf("line %d: unknown API section %q", number+1, key)
			}
			continue
		}
		return profile, fmt.Errorf("line %d: unexpected nested key %q", number+1, key)
	}
	if profile.version != 1 {
		return profile, fmt.Errorf("unsupported version %d", profile.version)
	}
	if !seen["engines"] || !seen["workers"] || !seen["apis"] {
		return profile, fmt.Errorf("profile must define engines, workers and apis")
	}
	return profile, nil
}

func isolationBuildPlan(profile profileDocument) ([]string, error) {
	selected := []string{"core"}
	for _, group := range []struct {
		prefix string
		values []string
	}{
		{"engine.", profile.engines},
		{"worker.", profile.workers},
		{"admin.", profile.admin},
		{"public.", profile.public},
	} {
		for _, name := range group.values {
			selected = append(selected, group.prefix+name)
		}
	}
	if profile.devFixture {
		selected = append(selected, "development.fixtures")
	}
	registry := isolationProfileRegistry()
	active := make(map[string]bool, len(selected))
	for _, component := range selected {
		entry, ok := registry[component]
		if !ok || entry.id != component {
			return nil, fmt.Errorf("unknown component %q", component)
		}
		if active[component] {
			return nil, fmt.Errorf("duplicate component %q", component)
		}
		active[component] = true
	}
	for _, component := range selected {
		for _, dependency := range registry[component].requires {
			if !active[dependency] {
				return nil, fmt.Errorf("component %q requires %q", component, dependency)
			}
		}
	}
	sort.Strings(selected)
	ordered := make([]string, 0, len(selected))
	visited := make(map[string]bool, len(selected))
	var visit func(string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		dependencies := append([]string(nil), registry[id].requires...)
		sort.Strings(dependencies)
		for _, dependency := range dependencies {
			visit(dependency)
		}
		ordered = append(ordered, id)
	}
	for _, id := range selected {
		visit(id)
	}
	return ordered, nil
}

type generatedProfile struct {
	packageName string
	buildTag    string
	plan        []string
	devFixtures bool
	imports     map[string]bool
}

func parseGeneratedProfile(path string) (generatedProfile, error) {
	var result generatedProfile
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		return result, err
	}
	result.packageName = file.Name.Name
	result.imports = make(map[string]bool)
	for _, imported := range file.Imports {
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return result, err
		}
		result.imports[value] = true
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if text := strings.TrimPrefix(comment.Text, "//go:build "); text != comment.Text {
				result.buildTag = strings.TrimSpace(text)
			}
		}
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch function.Name.Name {
		case "GeneratedBuildPlan":
			result.plan, err = generatedPlanFromFunction(function)
			if err != nil {
				return result, err
			}
		case "GeneratedDevelopmentFixtures":
			result.devFixtures, err = generatedBoolFromFunction(function)
			if err != nil {
				return result, err
			}
		}
	}
	if result.plan == nil {
		return result, fmt.Errorf("GeneratedBuildPlan is missing")
	}
	if result.buildTag == "" {
		return result, fmt.Errorf("//go:build constraint is missing")
	}
	return result, nil
}

func generatedPlanFromFunction(function *ast.FuncDecl) ([]string, error) {
	var values []string
	var found bool
	ast.Inspect(function.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, element := range literal.Elts {
			keyed, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			identifier, ok := keyed.Key.(*ast.Ident)
			if !ok || identifier.Name != "Components" {
				continue
			}
			list, ok := keyed.Value.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, item := range list.Elts {
				basic, ok := item.(*ast.BasicLit)
				if !ok || basic.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(basic.Value)
				if err != nil {
					continue
				}
				values = append(values, value)
			}
			found = true
		}
		return true
	})
	if !found {
		return nil, fmt.Errorf("GeneratedBuildPlan has no Components literal")
	}
	return values, nil
}

func generatedBoolFromFunction(function *ast.FuncDecl) (bool, error) {
	var value bool
	var found bool
	ast.Inspect(function.Body, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		switch identifier.Name {
		case "true":
			value, found = true, true
		case "false":
			value, found = false, true
		}
		return true
	})
	if !found {
		return false, fmt.Errorf("GeneratedDevelopmentFixtures has no boolean literal")
	}
	return value, nil
}

func mustGlob(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
