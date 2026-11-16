package sitebuilder

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tdewolff/parse/v2"
	cssparse "github.com/tdewolff/parse/v2/css"
	jsparse "github.com/tdewolff/parse/v2/js"
	"golang.org/x/net/html"
	htmlatom "golang.org/x/net/html/atom"
)

var templateExpressionPattern = regexp.MustCompile(`(?s)\{\{.*?\}\}|\$\{.*?\}|<%.*?%>|\{%.*?%\}`)

var staticHTMLTags = stringSet(
	"a", "abbr", "address", "article", "aside", "b", "blockquote", "br", "button", "caption", "cite", "code", "dd", "del", "details", "div", "dl", "dt", "em", "figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "i", "img", "ins", "kbd", "li", "main", "mark", "ol", "p", "picture", "pre", "q", "s", "samp", "section", "small", "source", "span", "strong", "sub", "summary", "sup", "table", "tbody", "td", "tfoot", "th", "thead", "time", "tr", "u", "ul", "video", "audio", "track",
)

var staticSVGTags = stringSet(
	"svg", "g", "path", "circle", "ellipse", "rect", "line", "polyline", "polygon", "title", "desc", "defs", "use", "clippath", "mask", "lineargradient", "radialgradient", "stop", "symbol",
)

var staticGlobalAttributes = stringSet("id", "class", "title", "role", "lang", "dir", "tabindex", "hidden", "draggable")
var staticResourceAttributes = stringSet("src", "poster")
var staticHTMLURLSchemes = stringSet("https", "http", "mailto", "tel")
var staticCSSAtRules = stringSet("@media", "@supports", "@container", "@layer", "@keyframes", "@-webkit-keyframes", "@font-face", "@charset", "@starting-style", "@counter-style", "@property")
var srcsetDescriptorPattern = regexp.MustCompile(`^(?:[1-9][0-9]*(?:\.[0-9]+)?x|[1-9][0-9]*w)$`)

const maxStaticHTMLBytes = 2 << 20

func stringSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func validateStaticFeatureAssets(manifest Manifest, assets []Asset, assetPaths map[string]bool) error {
	assetByPath := make(map[string]Asset, len(assets))
	for _, asset := range assets {
		assetByPath[asset.Path] = asset
	}
	if manifest.Runtime.ABI == RuntimeABIStaticModule {
		if err := validateModuleImports(assetByPath); err != nil {
			return err
		}
	}
	if manifest.Runtime.ABI == RuntimeABIStaticHTML {
		entry := assetByPath[manifest.Runtime.Entry]
		if len(entry.Data) > maxStaticHTMLBytes {
			return fmt.Errorf("static HTML entry exceeds %d bytes", maxStaticHTMLBytes)
		}
		if err := validateHTMLFragment(entry.Data, manifest.Runtime.Entry, assetPaths); err != nil {
			return fmt.Errorf("static HTML entry %q: %w", manifest.Runtime.Entry, err)
		}
	}
	for _, stylePath := range manifest.Runtime.Styles {
		style := assetByPath[stylePath]
		if err := validateScopedStylesheet(style.Data, stylePath, manifest.Key, assetPaths); err != nil {
			return fmt.Errorf("runtime stylesheet %q: %w", stylePath, err)
		}
	}
	return nil
}

func validateModuleImports(assets map[string]Asset) error {
	for _, asset := range assets {
		ext := strings.ToLower(path.Ext(asset.Path))
		if ext != ".js" && ext != ".mjs" {
			continue
		}
		ast, err := jsparse.Parse(parse.NewInput(bytes.NewReader(asset.Data)), jsparse.Options{})
		if err != nil {
			return fmt.Errorf("JavaScript asset %q cannot be parsed: %w", asset.Path, err)
		}
		validator := &moduleImportValidator{owner: asset.Path, assets: assets}
		jsparse.Walk(validator, ast)
		if validator.err != nil {
			return fmt.Errorf("JavaScript asset %q: %w", asset.Path, validator.err)
		}
	}
	return nil
}

type moduleImportValidator struct {
	owner  string
	assets map[string]Asset
	err    error
}

func (v *moduleImportValidator) Enter(node jsparse.INode) jsparse.IVisitor {
	if v.err != nil {
		return nil
	}
	switch node := node.(type) {
	case *jsparse.ImportStmt:
		v.err = v.validateSpecifier(node.Module, "import declaration")
	case *jsparse.ExportStmt:
		if node.Module != nil {
			v.err = v.validateSpecifier(node.Module, "export declaration")
		}
	case *jsparse.CallExpr:
		literal, isImport := node.X.(*jsparse.LiteralExpr)
		if isImport && literal.TokenType == jsparse.ImportToken {
			if len(node.Args.List) != 1 || node.Args.List[0].Rest {
				v.err = fmt.Errorf("dynamic import must use one literal local module path")
				return nil
			}
			specifier, isString := node.Args.List[0].Value.(*jsparse.LiteralExpr)
			if !isString || specifier.TokenType != jsparse.StringToken {
				v.err = fmt.Errorf("dynamic import must use one literal local module path")
				return nil
			}
			v.err = v.validateSpecifier(specifier.Data, "dynamic import")
		}
	}
	return v
}

func (v *moduleImportValidator) Exit(jsparse.INode) {}

func (v *moduleImportValidator) validateSpecifier(raw []byte, declaration string) error {
	specifier, err := decodeModuleString(string(raw))
	if err != nil {
		return fmt.Errorf("%s: %w", declaration, err)
	}
	if err := validateModuleSpecifier(v.owner, specifier, v.assets); err != nil {
		return fmt.Errorf("%s: %w", declaration, err)
	}
	return nil
}

func decodeModuleString(raw string) (string, error) {
	value, err := strconv.Unquote(raw)
	if err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("module specifier must be a non-empty literal string")
	}
	return value, nil
}

func validateModuleSpecifier(owner, specifier string, assets map[string]Asset) error {
	if strings.Contains(specifier, "\\") {
		return fmt.Errorf("module import %q must use a relative archive path", specifier)
	}
	u, err := url.Parse(specifier)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || strings.HasPrefix(u.Path, "/") {
		return fmt.Errorf("module import %q must be a relative archive path", specifier)
	}
	if !strings.HasPrefix(u.Path, "./") && !strings.HasPrefix(u.Path, "../") {
		return fmt.Errorf("bare module import %q is not allowed; bundle dependencies into the ZIP", specifier)
	}
	resolved, err := resolveArchiveReference(owner, u.Path)
	if err != nil {
		return fmt.Errorf("module import %q is invalid: %w", specifier, err)
	}
	if _, ok := assets[resolved]; !ok {
		return fmt.Errorf("module import %q refers to missing asset %q", specifier, resolved)
	}
	if ext := strings.ToLower(path.Ext(resolved)); ext != ".js" && ext != ".mjs" {
		return fmt.Errorf("module import %q must resolve to JavaScript", specifier)
	}
	return nil
}

func validateScopedStylesheet(source []byte, owner string, featureKey string, assetPaths map[string]bool) error {
	if !utf8.Valid(source) {
		return fmt.Errorf("stylesheet must be UTF-8")
	}
	parser := cssparse.NewParser(parse.NewInput(bytes.NewReader(source)), false)
	var atRules []string
	for {
		grammar, _, data := parser.Next()
		if grammar == cssparse.ErrorGrammar {
			if err := parser.Err(); err != nil && err != io.EOF {
				return fmt.Errorf("invalid CSS: %w", err)
			}
			break
		}
		name := strings.ToLower(strings.TrimSpace(string(data)))
		switch grammar {
		case cssparse.AtRuleGrammar, cssparse.BeginAtRuleGrammar:
			atName := strings.Fields(name)
			if len(atName) == 0 || !staticCSSAtRules[atName[0]] {
				return fmt.Errorf("at-rule %q is not allowed in isolated feature styles", name)
			}
			parameters := strings.Fields(cssTokensRaw(parser.Values()))
			if atName[0] == "@charset" && (len(parameters) == 0 || !strings.Contains(strings.ToLower(parameters[0]), "utf-8")) {
				return fmt.Errorf("stylesheet charset must be UTF-8")
			}
			if atName[0] == "@keyframes" || atName[0] == "@-webkit-keyframes" || atName[0] == "@counter-style" {
				if len(parameters) == 0 || !strings.HasPrefix(strings.Trim(parameters[0], `"'`), featureKey+"-") {
					return fmt.Errorf("global CSS name in %q must begin with %q", name, featureKey+"-")
				}
			}
			if atName[0] == "@layer" && len(parameters) > 0 && !strings.HasPrefix(parameters[0], featureKey+"-") {
				return fmt.Errorf("named CSS layers must begin with %q", featureKey+"-")
			}
			if atName[0] == "@property" && (len(parameters) == 0 || !strings.HasPrefix(parameters[0], "--feature-"+featureKey+"-")) {
				return fmt.Errorf("registered custom properties must begin with %q", "--feature-"+featureKey+"-")
			}
			if grammar == cssparse.BeginAtRuleGrammar {
				atRules = append(atRules, atName[0])
			}
		case cssparse.EndAtRuleGrammar:
			if len(atRules) > 0 {
				atRules = atRules[:len(atRules)-1]
			}
		case cssparse.BeginRulesetGrammar, cssparse.QualifiedRuleGrammar:
			if !insideKeyframes(atRules) {
				selector := cssTokensRaw(parser.Values())
				if err := validateScopedSelectorList(selector, featureKey); err != nil {
					return err
				}
			}
		case cssparse.DeclarationGrammar:
			if len(atRules) > 0 && atRules[len(atRules)-1] == "@font-face" && strings.EqualFold(strings.TrimSpace(string(data)), "font-family") {
				fontFamily := strings.Trim(cssTokensRaw(parser.Values()), `"' `)
				if !strings.HasPrefix(fontFamily, featureKey+"-") {
					return fmt.Errorf("@font-face font-family must begin with %q", featureKey+"-")
				}
			}
		}
		for _, token := range parser.Values() {
			if token.TokenType != cssparse.URLToken {
				continue
			}
			reference, err := cssURLReference(token.Data)
			if err != nil {
				return err
			}
			if strings.HasPrefix(reference, "#") {
				continue
			}
			if _, err := resolveExistingArchiveAsset(owner, reference, assetPaths); err != nil {
				return fmt.Errorf("CSS resource %q: %w", reference, err)
			}
		}
	}
	if parser.HasParseError() {
		return fmt.Errorf("invalid CSS syntax")
	}
	return nil
}

func insideKeyframes(atRules []string) bool {
	for i := len(atRules) - 1; i >= 0; i-- {
		if atRules[i] == "@keyframes" || atRules[i] == "@-webkit-keyframes" {
			return true
		}
	}
	return false
}

func cssTokensRaw(tokens []cssparse.Token) string {
	var result strings.Builder
	for _, token := range tokens {
		result.Write(token.Data)
	}
	return result.String()
}

func validateScopedSelectorList(selector string, featureKey string) error {
	scopes := []string{
		`[data-engine-feature="` + featureKey + `"]`,
		`[data-engine-feature='` + featureKey + `']`,
		`[data-engine-feature=` + featureKey + `]`,
	}
	for _, part := range splitCSSSelectorList(selector) {
		part = strings.TrimSpace(part)
		if !hasFeatureScopePrefix(part, scopes) {
			return fmt.Errorf("selector %q must be scoped beneath %s", part, scopes[0])
		}
	}
	return nil
}

func hasFeatureScopePrefix(selector string, scopes []string) bool {
	for _, scope := range scopes {
		if strings.HasPrefix(selector, scope) || strings.HasPrefix(selector, ":where("+scope+")") {
			return true
		}
	}
	return false
}

func splitCSSSelectorList(input string) []string {
	var result []string
	start, parens, brackets := 0, 0, 0
	var quote rune
	escaped := false
	for i, r := range input {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '(':
			parens++
		case ')':
			parens--
		case '[':
			brackets++
		case ']':
			brackets--
		case ',':
			if parens == 0 && brackets == 0 {
				result = append(result, input[start:i])
				start = i + 1
			}
		}
	}
	result = append(result, input[start:])
	return result
}

func cssURLReference(raw []byte) (string, error) {
	value := strings.TrimSpace(string(raw))
	if len(value) < 5 || !strings.EqualFold(value[:4], "url(") || value[len(value)-1] != ')' {
		return "", fmt.Errorf("invalid CSS URL token")
	}
	value = strings.TrimSpace(value[4 : len(value)-1])
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	if value == "" || strings.ContainsAny(value, "\\\n\r\t") {
		return "", fmt.Errorf("CSS URLs must be non-empty and unescaped")
	}
	return value, nil
}

func resolveExistingArchiveAsset(owner, reference string, assetPaths map[string]bool) (string, error) {
	resolved, err := resolveArchiveReference(owner, reference)
	if err != nil {
		return "", err
	}
	if !assetPaths[resolved] {
		return "", fmt.Errorf("missing archive asset %q", resolved)
	}
	return resolved, nil
}

func resolveArchiveReference(owner, reference string) (string, error) {
	if strings.Contains(reference, "\\") {
		return "", fmt.Errorf("reference must use a relative archive path")
	}
	u, err := url.Parse(reference)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
		return "", fmt.Errorf("reference must be relative to its archive file")
	}
	resolved := path.Clean(path.Join(path.Dir(owner), u.Path))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") || !validAssetPath(resolved) {
		return "", fmt.Errorf("reference escapes the archive root")
	}
	return resolved, nil
}

func validateHTMLFragment(source []byte, owner string, assetPaths map[string]bool) error {
	if !utf8.Valid(source) {
		return fmt.Errorf("fragment must be UTF-8")
	}
	if templateExpressionPattern.Match(source) {
		return fmt.Errorf("template expressions are not supported")
	}
	tokenizer := html.NewTokenizer(bytes.NewReader(source))
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			if err := tokenizer.Err(); err != nil && err != io.EOF {
				return fmt.Errorf("invalid HTML: %w", err)
			}
			break
		}
		token := tokenizer.Token()
		if tokenType == html.DoctypeToken {
			return fmt.Errorf("entry must be an HTML fragment, not a document")
		}
		if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken && tokenType != html.EndTagToken {
			continue
		}
		tag := strings.ToLower(token.Data)
		if tag == "html" || tag == "head" || tag == "body" {
			return fmt.Errorf("entry must be an HTML fragment; <%s> is not allowed", tag)
		}
		for _, attr := range token.Attr {
			if strings.HasPrefix(strings.ToLower(attr.Key), "on") || strings.EqualFold(attr.Key, "style") || strings.EqualFold(attr.Key, "srcdoc") {
				return fmt.Errorf("attribute %q is not allowed in static HTML", attr.Key)
			}
		}
	}
	context := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: htmlatom.Div}
	nodes, err := html.ParseFragment(bytes.NewReader(source), context)
	if err != nil {
		return fmt.Errorf("parse HTML fragment: %w", err)
	}
	stack := append([]*html.Node(nil), nodes...)
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			stack = append(stack, child)
		}
		if node.Type != html.ElementNode {
			continue
		}
		tag := strings.ToLower(node.Data)
		if node.Namespace == "" && tag == "main" {
			return fmt.Errorf("feature fragments must not define <main>; the page renderer owns the single page landmark")
		}
		if node.Namespace == "" {
			if !staticHTMLTags[tag] {
				return fmt.Errorf("element <%s> is not allowed in static HTML", tag)
			}
		} else if node.Namespace == "svg" {
			if !staticSVGTags[tag] {
				return fmt.Errorf("SVG element <%s> is not allowed in static HTML", tag)
			}
		} else {
			return fmt.Errorf("foreign content is not allowed in static HTML")
		}
		for _, attr := range node.Attr {
			name := strings.ToLower(attr.Key)
			if attr.Namespace != "" || strings.HasPrefix(name, "on") || name == "style" || name == "srcdoc" || name == "form" || name == "formaction" || name == "action" {
				return fmt.Errorf("attribute %q is not allowed in static HTML", attr.Key)
			}
			if node.Namespace == "svg" && name == "xmlns" && attr.Val != "http://www.w3.org/2000/svg" {
				return fmt.Errorf("SVG xmlns must use the standard SVG namespace")
			}
			if strings.HasPrefix(name, "aria-") || strings.HasPrefix(name, "data-") || staticGlobalAttributes[name] || safeStaticAttribute(tag, name) {
				if name == "href" {
					if node.Namespace == "svg" {
						if !strings.HasPrefix(strings.TrimSpace(attr.Val), "#") {
							return fmt.Errorf("SVG href must refer to a local fragment")
						}
					} else if err := validateStaticLink(attr.Val); err != nil {
						return err
					}
				}
				if staticResourceAttributes[name] {
					if _, err := resolveExistingArchiveAsset(owner, attr.Val, assetPaths); err != nil {
						return fmt.Errorf("resource %q: %w", attr.Val, err)
					}
				}
				if name == "srcset" {
					if err := validateStaticSrcset(owner, attr.Val, assetPaths); err != nil {
						return err
					}
				}
				if (name == "fill" || name == "stroke" || name == "filter" || name == "clip-path" || name == "mask") && strings.Contains(strings.ToLower(attr.Val), "url(") {
					value := strings.TrimSpace(attr.Val)
					if !strings.HasPrefix(strings.ToLower(value), "url(#") || !strings.HasSuffix(value, ")") {
						return fmt.Errorf("SVG paint and filter URLs must refer to a local fragment")
					}
				}
				continue
			}
			if node.Namespace == "svg" && safeSVGAttribute(name) {
				continue
			}
			return fmt.Errorf("attribute %q is not allowed on <%s>", name, tag)
		}
		if node.Namespace == "" && strings.EqualFold(tag, "a") && staticAttributeValue(node.Attr, "target") == "_blank" {
			rel := strings.Fields(staticAttributeValue(node.Attr, "rel"))
			if !containsString(rel, "noopener") && !containsString(rel, "noreferrer") {
				return fmt.Errorf("links with target=\"_blank\" must include rel=\"noopener\" or rel=\"noreferrer\"")
			}
		}
	}
	return nil
}

func safeStaticAttribute(tag, name string) bool {
	if name == "href" {
		return tag == "a" || tag == "area" || tag == "use"
	}
	if name == "srcset" {
		return tag == "img" || tag == "source"
	}
	switch tag {
	case "img":
		return stringSet("alt", "width", "height", "loading", "decoding", "src")[name]
	case "source":
		return stringSet("src", "type", "media", "sizes")[name]
	case "video":
		return stringSet("src", "poster", "controls", "loop", "muted", "preload", "playsinline", "width", "height")[name]
	case "audio":
		return stringSet("src", "controls", "loop", "muted", "preload")[name]
	case "track":
		return stringSet("src", "kind", "srclang", "label", "default")[name]
	case "button":
		return stringSet("type", "disabled")[name]
	case "details":
		return name == "open"
	case "td", "th":
		return stringSet("colspan", "rowspan", "headers", "scope")[name]
	case "blockquote", "q":
		return name == "cite"
	}
	return false
}

func safeSVGAttribute(name string) bool {
	return stringSet("viewbox", "xmlns", "fill", "fill-rule", "stroke", "stroke-width", "stroke-linecap", "stroke-linejoin", "stroke-dasharray", "stroke-dashoffset", "opacity", "d", "cx", "cy", "r", "rx", "ry", "x", "x1", "x2", "y", "y1", "y2", "width", "height", "points", "transform", "preserveaspectratio", "offset", "stop-color", "stop-opacity", "gradientunits", "gradienttransform", "clip-rule", "focusable")[name]
}

func validateStaticLink(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("link URL must be non-empty and contain no control characters")
	}
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid link URL %q", raw)
	}
	if u.Scheme != "" && !staticHTMLURLSchemes[strings.ToLower(u.Scheme)] {
		return fmt.Errorf("link URL scheme %q is not allowed", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("links with embedded credentials are not allowed")
	}
	return nil
}

func validateStaticSrcset(owner, value string, assetPaths map[string]bool) error {
	for _, candidate := range strings.Split(value, ",") {
		fields := strings.Fields(strings.TrimSpace(candidate))
		if len(fields) == 0 || len(fields) > 2 {
			return fmt.Errorf("invalid srcset candidate")
		}
		if _, err := resolveExistingArchiveAsset(owner, fields[0], assetPaths); err != nil {
			return fmt.Errorf("srcset resource %q: %w", fields[0], err)
		}
		if len(fields) == 2 && !srcsetDescriptorPattern.MatchString(fields[1]) {
			return fmt.Errorf("invalid srcset descriptor %q", fields[1])
		}
	}
	return nil
}

func staticAttributeValue(attributes []html.Attribute, key string) string {
	for _, attribute := range attributes {
		if strings.EqualFold(attribute.Key, key) {
			return attribute.Val
		}
	}
	return ""
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
