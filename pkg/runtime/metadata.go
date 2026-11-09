package runtime

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"io/fs"
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

// HeadMetadata is the public metadata rendered into the server document.
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

type publicDocumentTemplateData struct {
	Metadata      HeadMetadata
	ScriptURL     string
	StylesheetURL string
	ShellCSS      htmltemplate.CSS
	Groups        placementRootGroups
	Bootstrap     htmltemplate.JS
}

// ErrorPageTitle returns the localized title used for a system HTTP response.
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

// RenderPageDocument builds the complete public HTML document in one named Go
// template execution. Its inputs are resolved server state, typed placements,
// metadata and JSON; callers never splice markup into a partially rendered
// shell.
func RenderPageDocument(metadata HeadMetadata, pageKind, snapshotID string, revision int64, placements []PlacementRoot, payload []byte) ([]byte, error) {
	if metadata.FaviconURL == "" {
		metadata.FaviconURL = "/favicon.svg"
	}
	if metadata.OGTitle == "" {
		metadata.OGTitle = metadata.Title
	}
	if metadata.OGDescription == "" {
		metadata.OGDescription = metadata.Description
	}

	groups, err := groupPlacementRoots(pageKind, snapshotID, revision, placements)
	if err != nil {
		return nil, err
	}
	bootstrap, err := safeBootstrapJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("encode public runtime bootstrap: %w", err)
	}
	scriptURL, stylesheetURL, err := publicRuntimeAssets()
	if err != nil {
		return nil, err
	}
	shellCSS, err := embeddedShellStyles(stylesheetURL)
	if err != nil {
		return nil, err
	}
	if len(shellCSS) > 0 {
		stylesheetURL = ""
	}

	parsed, err := loadRuntimeTemplates()
	if err != nil {
		return nil, err
	}
	var rendered bytes.Buffer
	data := publicDocumentTemplateData{
		Metadata: metadata, ScriptURL: scriptURL, StylesheetURL: stylesheetURL,
		ShellCSS: htmltemplate.CSS(shellCSS), Groups: groups, Bootstrap: bootstrap,
	}
	if err := parsed.ExecuteTemplate(&rendered, "publicDocument", data); err != nil {
		return nil, fmt.Errorf("render public document template: %w", err)
	}
	return rendered.Bytes(), nil
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

func embeddedShellStyles(stylesheetURL string) ([]byte, error) {
	if stylesheetURL == "" || !strings.HasPrefix(stylesheetURL, "/") || strings.Contains(stylesheetURL, "..") {
		return nil, fmt.Errorf("invalid embedded runtime stylesheet path")
	}
	css, err := fs.ReadFile(embedded, "dist"+stylesheetURL)
	if err != nil {
		return nil, fmt.Errorf("read embedded public runtime stylesheet: %w", err)
	}
	if len(css) > 8*1024 || bytes.Contains(bytes.ToLower(css), []byte("</style")) {
		return nil, nil
	}
	return css, nil
}

func safeBootstrapJSON(payload []byte) (htmltemplate.JS, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", fmt.Errorf("bootstrap contains more than one JSON value")
		}
		return "", fmt.Errorf("bootstrap has trailing data: %w", err)
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	// encoding/json escapes characters that could end the inert script element.
	return htmltemplate.JS(bytes.TrimSpace(encoded.Bytes())), nil
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
