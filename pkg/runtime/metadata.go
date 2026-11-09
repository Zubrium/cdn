package runtime

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"regexp"
	"strings"
	"sync"
)

//go:embed templates/*.tmpl
var templateFiles embed.FS

var (
	runtimeTemplatesOnce sync.Once
	runtimeTemplates     *htmltemplate.Template
	runtimeTemplatesErr  error
)

// HeadMetadata is the small public metadata contract shared with the page
// composer. Values are escaped before being written into the HTML document.
type HeadMetadata struct {
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
	FaviconURL    string `json:"faviconUrl,omitempty"`
	OGTitle       string `json:"ogTitle,omitempty"`
	OGDescription string `json:"ogDescription,omitempty"`
	OGImageURL    string `json:"ogImageUrl,omitempty"`
	CanonicalURL  string `json:"canonicalUrl,omitempty"`
	Robots        string `json:"robots,omitempty"`
	Language      string `json:"language,omitempty"`
}

func ErrorPageTitle(status int) string {
	switch status {
	case 400:
		return "Запрос не выполнен"
	case 403:
		return "Доступ ограничен"
	case 404:
		return "Страница не найдена"
	case 503:
		return "Сайт ещё не настроен"
	default:
		return "Сайт временно недоступен"
	}
}

var shellStylesheetTag = regexp.MustCompile(`(?is)<link\b[^>]*\brel\s*=\s*["']stylesheet["'][^>]*>`)
var shellStylesheetHref = regexp.MustCompile(`(?i)\bhref\s*=\s*["']([^"']+\.css)["']`)

func RenderShell(metadata HeadMetadata) ([]byte, error) {
	if metadata.FaviconURL == "" {
		metadata.FaviconURL = "/favicon.svg"
	}
	if metadata.OGTitle == "" {
		metadata.OGTitle = metadata.Title
	}
	if metadata.OGDescription == "" {
		metadata.OGDescription = metadata.Description
	}
	parsed, err := loadRuntimeTemplates()
	if err != nil {
		return nil, err
	}
	scriptURL, stylesheetURL, err := publicRuntimeAssets()
	if err != nil {
		return nil, err
	}
	var rendered bytes.Buffer
	data := struct {
		Metadata      HeadMetadata
		ScriptURL     string
		StylesheetURL string
	}{Metadata: metadata, ScriptURL: scriptURL, StylesheetURL: stylesheetURL}
	if err := parsed.ExecuteTemplate(&rendered, "publicDocument", data); err != nil {
		return nil, fmt.Errorf("render public document template: %w", err)
	}
	return inlineEmbeddedShellStyles(rendered.Bytes()), nil
}

func publicRuntimeAssets() (scriptURL, stylesheetURL string, err error) {
	entries, err := fs.ReadDir(embedded, "dist/assets")
	if err != nil {
		return "", "", fmt.Errorf("read embedded public runtime assets: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, "public-runtime-") && strings.HasSuffix(name, ".js") {
			if scriptURL != "" {
				return "", "", fmt.Errorf("multiple public runtime entry scripts are embedded")
			}
			scriptURL = "/assets/" + name
		}
		if !entry.IsDir() && strings.HasPrefix(name, "public-runtime") && strings.HasSuffix(name, ".css") {
			if stylesheetURL != "" {
				return "", "", fmt.Errorf("multiple public runtime stylesheets are embedded")
			}
			stylesheetURL = "/assets/" + name
		}
	}
	if stylesheetURL == "" {
		if _, statErr := fs.Stat(embedded, "dist/public-runtime.css"); statErr == nil {
			stylesheetURL = "/public-runtime.css"
		}
	}
	if scriptURL == "" || stylesheetURL == "" {
		return "", "", fmt.Errorf("embedded public runtime entry or stylesheet is missing")
	}
	return scriptURL, stylesheetURL, nil
}

// inlineEmbeddedShellStyles embeds the small, app-owned base stylesheet so it
// does not add a render-blocking request before the server-rendered page is
// painted. Feature styles are handled separately by the prerender worker.
func inlineEmbeddedShellStyles(document []byte) []byte {
	match := shellStylesheetTag.FindIndex(document)
	if match == nil {
		return document
	}
	tag := document[match[0]:match[1]]
	href := shellStylesheetHref.FindSubmatch(tag)
	if len(href) != 2 {
		return document
	}
	asset := string(href[1])
	if asset != "/public-runtime.css" && asset != "/assets/public-runtime.css" &&
		!(strings.HasPrefix(asset, "/assets/public-runtime-") && strings.HasSuffix(asset, ".css")) {
		return document
	}
	css, err := fs.ReadFile(embedded, "dist"+asset)
	if err != nil || len(css) > 8*1024 || bytes.Contains(bytes.ToLower(css), []byte("</style")) {
		return document
	}
	parsed, err := loadRuntimeTemplates()
	if err != nil {
		return document
	}
	var style bytes.Buffer
	if err := parsed.ExecuteTemplate(&style, "publicShellStyle", htmltemplate.CSS(css)); err != nil {
		return document
	}
	result := make([]byte, 0, len(document)-len(tag)+style.Len())
	result = append(result, document[:match[0]]...)
	result = append(result, style.Bytes()...)
	result = append(result, document[match[1]:]...)
	return result
}

func loadRuntimeTemplates() (*htmltemplate.Template, error) {
	runtimeTemplatesOnce.Do(func() {
		runtimeTemplates, runtimeTemplatesErr = htmltemplate.ParseFS(templateFiles, "templates/*.tmpl")
	})
	if runtimeTemplatesErr != nil {
		return nil, fmt.Errorf("parse public runtime templates: %w", runtimeTemplatesErr)
	}
	return runtimeTemplates, nil
}
