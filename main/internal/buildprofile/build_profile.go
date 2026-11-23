package buildprofile

import (
	"bytes"
	"errors"
	"fmt"
	"go/build/constraint"
	"go/format"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	platform "github.com/Zubrium/cdn/core/presentation"
	"gopkg.in/yaml.v3"
)

const maxProfileBytes = 1 << 20

type BuildProfile struct {
	Version             int      `yaml:"version"`
	Engines             []string `yaml:"engines"`
	Workers             []string `yaml:"workers"`
	Composition         []string `yaml:"composition"`
	DevelopmentFixtures bool     `yaml:"development_fixtures"`
	APIs                struct {
		Admin  []string `yaml:"admin"`
		Public []string `yaml:"public"`
	} `yaml:"apis"`
}

type BuildPlan = platform.BuildPlan

func buildRegistry() Registry {
	return Registry{
		"core":                   {ID: "core"},
		"engine.loader":          {ID: "engine.loader", Requires: []string{"core"}},
		"engine.website":         {ID: "engine.website", Requires: []string{"core"}},
		"engine.content":         {ID: "engine.content", Requires: []string{"core", "engine.website"}},
		"engine.forms":           {ID: "engine.forms", Requires: []string{"core"}},
		"engine.router":          {ID: "engine.router", Requires: []string{"core"}},
		"engine.publication":     {ID: "engine.publication", Requires: []string{"core", "engine.website", "engine.content", "engine.router"}},
		"composition.retirement": {ID: "composition.retirement", Requires: []string{"core", "engine.website", "engine.router"}},
		"development.fixtures":   {ID: "development.fixtures", Requires: []string{"core", "engine.website", "engine.content", "engine.forms", "admin.website", "admin.content", "admin.forms", "admin.identity"}},
		"worker.archive":         {ID: "worker.archive", Requires: []string{"core"}},
		"worker.media":           {ID: "worker.media", Requires: []string{"core"}},
		"worker.prerender":       {ID: "worker.prerender", Requires: []string{"core"}},
		"admin.files":            {ID: "admin.files", Requires: []string{"engine.loader"}},
		"admin.website":          {ID: "admin.website", Requires: []string{"engine.website", "admin.files"}},
		"admin.content":          {ID: "admin.content", Requires: []string{"engine.content", "admin.files"}},
		"admin.forms":            {ID: "admin.forms", Requires: []string{"engine.forms", "admin.files"}},
		"admin.router":           {ID: "admin.router", Requires: []string{"engine.router"}},
		"admin.coordinator":      {ID: "admin.coordinator", Requires: []string{"core", "admin.files"}},
		"admin.identity":         {ID: "admin.identity", Requires: []string{"core", "admin.files"}},
		"admin.notifications":    {ID: "admin.notifications", Requires: []string{"admin.identity", "admin.files"}},
		"admin.ui":               {ID: "admin.ui", Requires: []string{"core", "admin.files"}},
		"public.router":          {ID: "public.router", Requires: []string{"engine.router"}},
		"public.form-session":    {ID: "public.form-session", Requires: []string{"engine.forms", "public.router"}},
		"public.form-submit":     {ID: "public.form-submit", Requires: []string{"engine.forms", "public.router"}},
		"public.form-upload":     {ID: "public.form-upload", Requires: []string{"engine.loader", "public.router"}},
		"public.content-runtime": {ID: "public.content-runtime", Requires: []string{"engine.website", "engine.content", "engine.router", "public.router"}},
	}
}

// ParseBuildProfile validates a versioned profile and resolves only the
// components selected by the caller. Missing dependencies are errors.
func ParseBuildProfile(input io.Reader) (BuildPlan, error) {
	if input == nil {
		return BuildPlan{}, errors.New("profile input is required")
	}
	data, err := io.ReadAll(io.LimitReader(input, maxProfileBytes+1))
	if err != nil {
		return BuildPlan{}, fmt.Errorf("read profile: %w", err)
	}
	if len(data) > maxProfileBytes {
		return BuildPlan{}, errors.New("profile exceeds size limit")
	}
	if err := validateProfileYAML(data); err != nil {
		return BuildPlan{}, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var profile BuildProfile
	if err := decoder.Decode(&profile); err != nil {
		return BuildPlan{}, fmt.Errorf("decode profile: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return BuildPlan{}, errors.New("profile must contain exactly one YAML document")
	}
	if profile.Version != 1 {
		return BuildPlan{}, errors.New("unsupported profile version")
	}
	selected := []string{"core"}
	for _, group := range []struct {
		prefix string
		values []string
	}{
		{"engine.", profile.Engines}, {"worker.", profile.Workers}, {"composition.", profile.Composition},
		{"admin.", profile.APIs.Admin}, {"public.", profile.APIs.Public},
	} {
		for _, name := range group.values {
			selected = append(selected, group.prefix+name)
		}
	}
	if profile.DevelopmentFixtures {
		selected = append(selected, "development.fixtures")
	}
	registry := buildRegistry()
	if err := registry.Validate(selected); err != nil {
		return BuildPlan{}, err
	}
	return canonicalBuildPlan(selected)
}

func canonicalBuildPlan(selected []string) (BuildPlan, error) {
	registry := buildRegistry()
	if err := registry.Validate(selected); err != nil {
		return BuildPlan{}, err
	}
	active := make(map[string]bool, len(selected))
	for _, id := range selected {
		active[id] = true
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
		deps := append([]string(nil), registry[id].Requires...)
		// The shared Admin listener wires live engine services into its routes
		// during Start. Their factories must run before admin.files, but
		// profiles without those Admin APIs must not declare the dependencies.
		if id == "admin.files" {
			for _, engine := range []string{"engine.forms", "engine.website", "engine.router"} {
				if active[engine] {
					deps = append(deps, engine)
				}
			}
		}
		if id == "engine.router" {
			for _, engine := range []string{"engine.forms", "engine.content"} {
				if active[engine] {
					deps = append(deps, engine)
				}
			}
		}
		sort.Strings(deps)
		for _, dep := range deps {
			visit(dep)
		}
		ordered = append(ordered, id)
	}
	for _, id := range selected {
		visit(id)
	}
	return BuildPlan{Components: ordered}, nil
}

var goPackageName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type generatedImport struct {
	path  string
	alias string
	blank bool
	reg   string
	value string
}

var generatedModuleImports = map[string]generatedImport{
	"engine.loader":          {path: "github.com/Zubrium/cdn/engines/loader/presentation", alias: "loaderEngine", reg: "loader"},
	"engine.website":         {path: "github.com/Zubrium/cdn/engines/website/presentation", alias: "websiteEngine", reg: "website"},
	"engine.content":         {path: "github.com/Zubrium/cdn/engines/content/presentation", alias: "contentEngine", reg: "content"},
	"engine.forms":           {path: "github.com/Zubrium/cdn/engines/forms/presentation", alias: "formsEngine", reg: "forms"},
	"engine.router":          {path: "github.com/Zubrium/cdn/engines/router/presentation", alias: "routerEngine", reg: "router"},
	"engine.publication":     {path: "github.com/Zubrium/cdn/main/internal/publication", alias: "publication", reg: "publication", value: "publication.Module{}"},
	"composition.retirement": {path: "github.com/Zubrium/cdn/main/internal/retirement", alias: "retirement", reg: "retirement", value: "retirement.Module{}"},
	"worker.archive":         {path: "github.com/Zubrium/cdn/workers/archive", alias: "archive", reg: "worker.archive", value: "archive.Descriptor{}"},
	"worker.media":           {path: "github.com/Zubrium/cdn/workers/media", alias: "media", reg: "worker.media", value: "media.Descriptor{}"},
	"worker.prerender":       {path: "github.com/Zubrium/cdn/workers/prerender", alias: "prerender", reg: "worker.prerender", value: "prerender.Descriptor{}"},
	"admin.files":            {path: "github.com/Zubrium/cdn/presentation/fileapi", blank: true},
	"admin.website":          {path: "github.com/Zubrium/cdn/presentation/sitesapi", blank: true},
	"admin.content":          {path: "github.com/Zubrium/cdn/presentation/admin/content", blank: true},
	"admin.forms":            {path: "github.com/Zubrium/cdn/presentation/admin/forms", blank: true},
	"admin.router":           {path: "github.com/Zubrium/cdn/presentation/admin/router", blank: true},
	"admin.coordinator":      {path: "github.com/Zubrium/cdn/presentation/admin/base/transport", blank: true},
	"admin.identity":         {path: "github.com/Zubrium/cdn/presentation/admin/base/transport", blank: true},
	"admin.notifications":    {path: "github.com/Zubrium/cdn/presentation/admin/base/transport", blank: true},
	"admin.ui":               {path: "github.com/Zubrium/cdn/presentation/adminui", blank: true},
	"public.router":          {path: "github.com/Zubrium/cdn/presentation/router", blank: true},
	"public.form-upload":     {path: "github.com/Zubrium/cdn/presentation/router/publicupload", blank: true},
	"public.form-session":    {path: "github.com/Zubrium/cdn/presentation/router/publicforms", blank: true},
	"public.form-submit":     {path: "github.com/Zubrium/cdn/presentation/router/publicforms", blank: true},
	"public.content-runtime": {path: "github.com/Zubrium/cdn/presentation/router/publiccontentruntime", blank: true},
	"development.fixtures":   {path: "github.com/Zubrium/cdn/main/internal/devfixtures", alias: "devfixtures", reg: "development.fixtures"},
}

// GenerateBuildProfileGo emits deterministic imports for only the selected
// components, their engine descriptors, and the canonical dependency plan.
func GenerateBuildProfileGo(plan BuildPlan, packageName string) ([]byte, error) {
	return GenerateBuildProfileGoWithTag(plan, packageName, "")
}

func GenerateBuildProfileGoWithTag(plan BuildPlan, packageName, buildTag string) ([]byte, error) {
	if !goPackageName.MatchString(packageName) || len(plan.Components) == 0 {
		return nil, platform.ErrComponentPlanInvalid
	}
	if buildTag != "" {
		if strings.ContainsAny(buildTag, "\r\n") {
			return nil, platform.ErrComponentPlanInvalid
		}
		if _, err := constraint.Parse("//go:build " + buildTag); err != nil {
			return nil, platform.ErrComponentPlanInvalid
		}
	}
	canonical, err := canonicalBuildPlan(append([]string(nil), plan.Components...))
	if err != nil {
		return nil, err
	}
	imports := []generatedImport{
		{path: "github.com/Zubrium/cdn/core/presentation", alias: "platform"},
	}
	registrations := make([]string, 0)
	for _, id := range canonical.Components {
		module, ok := generatedModuleImports[id]
		if !ok {
			continue
		}
		imports = append(imports, module)
		if module.reg != "" {
			value := module.value
			if value == "" {
				value = module.alias + ".Descriptor{}"
			}
			registrations = append(registrations, fmt.Sprintf("if err := registry.Register(%s, %s); err != nil { return err }", strconv.Quote(module.reg), value))
		}
	}
	sort.Slice(imports, func(i, j int) bool { return imports[i].path < imports[j].path })
	var source strings.Builder
	source.WriteString("// Code generated by core profilegen. DO NOT EDIT.\n")
	if buildTag != "" {
		fmt.Fprintf(&source, "//go:build %s\n", buildTag)
	}
	source.WriteString("\n")
	fmt.Fprintf(&source, "package %s\n\nimport (\n", packageName)
	lastPath := ""
	for _, item := range imports {
		if item.path == lastPath {
			continue
		}
		if item.blank {
			fmt.Fprintf(&source, "_ %s\n", strconv.Quote(item.path))
		} else {
			fmt.Fprintf(&source, "%s %s\n", item.alias, strconv.Quote(item.path))
		}
		lastPath = item.path
	}
	source.WriteString(")\n\nfunc RegisterGeneratedEngines(registry *ProfileRegistry) error {\nif registry == nil { return platform.ErrInvalid }\n")
	if len(registrations) == 0 {
		source.WriteString("return nil\n")
	} else {
		for _, registration := range registrations {
			source.WriteString(registration + "\n")
		}
		source.WriteString("return nil\n")
	}
	source.WriteString("}\n\nfunc GeneratedBuildPlan() platform.BuildPlan {\nreturn platform.BuildPlan{Components: []string{\n")
	for _, id := range canonical.Components {
		fmt.Fprintf(&source, "%s,\n", strconv.Quote(id))
	}
	source.WriteString("}}\n}\n")
	developmentFixtures := false
	for _, component := range canonical.Components {
		if component == "development.fixtures" {
			developmentFixtures = true
			break
		}
	}
	fmt.Fprintf(&source, "\nfunc GeneratedDevelopmentFixtures() bool { return %t }\n", developmentFixtures)
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated build profile: %w", err)
	}
	return formatted, nil
}

func validateProfileYAML(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode profile: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("profile must contain exactly one YAML document")
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("profile root must be a mapping")
	}
	root := document.Content[0]
	if err := validateProfileNode(root); err != nil {
		return err
	}
	for _, field := range []string{"engines", "workers"} {
		if err := requireProfileSequence(root, field); err != nil {
			return err
		}
	}
	apiNode := profileMappingValue(root, "apis")
	if apiNode == nil || apiNode.Kind != yaml.MappingNode {
		return errors.New("profile must explicitly define admin and public APIs")
	}
	for _, field := range []string{"admin", "public"} {
		if err := requireProfileSequence(apiNode, field); err != nil {
			return err
		}
	}
	return nil
}

func profileMappingValue(mapping *yaml.Node, name string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == name {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func requireProfileSequence(mapping *yaml.Node, name string) error {
	value := profileMappingValue(mapping, name)
	if value == nil || value.Kind != yaml.SequenceNode {
		return fmt.Errorf("profile must explicitly define %q as a list", name)
	}
	return nil
}

func validateProfileNode(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.AliasNode {
		return errors.New("YAML aliases are not supported in build profiles")
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Kind != yaml.ScalarNode || key.Tag == "!!merge" {
				return errors.New("profile mapping keys must be plain scalars")
			}
			if _, exists := seen[key.Value]; exists {
				return fmt.Errorf("duplicate profile key %q", key.Value)
			}
			seen[key.Value] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := validateProfileNode(child); err != nil {
			return err
		}
	}
	return nil
}
