package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
)

// PlacementRoot is the public, non-content portion of a placement required
// before its existing feature ABI is mounted by the browser runtime.
type PlacementRoot struct {
	ID         string
	Slot       string
	FeatureKey string
}

type placementRootGroups struct {
	PageKind        string
	SnapshotID      string
	ContentRevision int64
	Background      []PlacementRoot
	Header          []PlacementRoot
	Main            []PlacementRoot
	Footer          []PlacementRoot
}

// RenderPlacementRoots renders the shared page landmark and the neutral mount
// roots from the already-resolved route composition. It doesn't render or
// reinterpret feature content. document must be the application-owned static
// shell, and all dynamic placement values are contextually escaped.
func RenderPlacementRoots(document []byte, pageKind, snapshotID string, revision int64, placements []PlacementRoot) ([]byte, error) {
	groups := placementRootGroups{PageKind: pageKind, SnapshotID: snapshotID, ContentRevision: revision}
	for _, placement := range placements {
		switch placement.Slot {
		case "background":
			groups.Background = append(groups.Background, placement)
		case "header":
			groups.Header = append(groups.Header, placement)
		case "main":
			groups.Main = append(groups.Main, placement)
		case "footer":
			groups.Footer = append(groups.Footer, placement)
		default:
			return nil, fmt.Errorf("unknown public placement slot %q", placement.Slot)
		}
	}

	const root = `<div id="root"></div>`
	if bytes.Count(document, []byte(root)) != 1 {
		return nil, fmt.Errorf("public runtime shell must contain exactly one empty #root")
	}
	parsed, err := loadRuntimeTemplates()
	if err != nil {
		return nil, err
	}
	rootStart := bytes.Index(document, []byte(root))
	data := struct {
		Before template.HTML
		After  template.HTML
		Groups placementRootGroups
	}{
		Before: template.HTML(string(document[:rootStart])),
		After:  template.HTML(string(document[rootStart+len(root):])),
		Groups: groups,
	}
	var output bytes.Buffer
	if err := parsed.ExecuteTemplate(&output, "publicPlacementDocument", data); err != nil {
		return nil, fmt.Errorf("render public placement document template: %w", err)
	}
	return output.Bytes(), nil
}

// AppendRuntimeBootstrap adds the already resolved public composition to the
// document. It accepts JSON rather than arbitrary HTML and re-encodes it with
// HTML escaping before inserting it into the inert JSON script element.
func AppendRuntimeBootstrap(document, payload []byte) ([]byte, error) {
	value, err := decodeBootstrapJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("decode public runtime bootstrap: %w", err)
	}

	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode public runtime bootstrap: %w", err)
	}
	// JSON emitted by encoding/json is safe to place in an inert script node:
	// '<', '>', '&', U+2028 and U+2029 are escaped, so values cannot terminate
	// the script element. Mark only this generated JSON as trusted template
	// content; feature values never enter template.HTML or template.JS directly.
	var element bytes.Buffer
	parsed, err := loadRuntimeTemplates()
	if err != nil {
		return nil, err
	}
	if err := parsed.ExecuteTemplate(&element, "runtimeBootstrap", template.JS(bytes.TrimSpace(encoded.Bytes()))); err != nil {
		return nil, fmt.Errorf("render public runtime bootstrap: %w", err)
	}

	lower := bytes.ToLower(document)
	closingBody := []byte("</body>")
	index := bytes.LastIndex(lower, closingBody)
	if index < 0 {
		return nil, fmt.Errorf("public runtime document has no closing body tag")
	}
	result := make([]byte, 0, len(document)+element.Len()+1)
	result = append(result, document[:index]...)
	result = append(result, element.Bytes()...)
	result = append(result, document[index:]...)
	return result, nil
}

func decodeBootstrapJSON(payload []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("bootstrap contains more than one JSON value")
		}
		return nil, fmt.Errorf("bootstrap has trailing data: %w", err)
	}
	return value, nil
}
