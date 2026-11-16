package media

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"
)

var (
	safeSVGElements = map[string]bool{
		"svg": true, "g": true, "defs": true, "rect": true, "path": true,
		"circle": true, "ellipse": true, "line": true, "polyline": true,
		"polygon": true, "pattern": true, "use": true, "image": true,
		"mask": true, "clippath": true, "lineargradient": true,
		"radialgradient": true, "stop": true, "symbol": true,
		"title": true, "desc": true, "text": true, "tspan": true,
	}
	safeSVGAttributes = map[string]bool{
		"xmlns": true, "width": true, "height": true, "viewbox": true,
		"fill": true, "fill-rule": true, "stroke": true, "stroke-width": true,
		"stroke-linecap": true, "stroke-linejoin": true, "stroke-miterlimit": true,
		"opacity": true, "fill-opacity": true, "stroke-opacity": true,
		"x": true, "y": true, "x1": true, "y1": true, "x2": true, "y2": true,
		"cx": true, "cy": true, "r": true, "rx": true, "ry": true,
		"d": true, "points": true, "transform": true, "id": true,
		"preserveaspectratio": true, "patternunits": true,
		"patterncontentunits": true, "gradientunits": true,
		"gradienttransform": true, "offset": true, "stop-color": true,
		"stop-opacity": true, "spreadmethod": true, "clip-path": true,
		"clip-rule": true, "mask": true, "mask-type": true,
		"font-family": true, "font-size": true, "font-weight": true,
		"text-anchor": true, "dominant-baseline": true, "href": true,
	}
	svgURL       = regexp.MustCompile(`(?i)url\([^)]*\)`)
	localSVGURL  = regexp.MustCompile(`(?i)^url\(\s*#[a-z0-9_.:-]+\s*\)$`)
	localSVGRef  = regexp.MustCompile(`^#[A-Za-z_][A-Za-z0-9_.:-]*$`)
	dataImageURL = regexp.MustCompile(`(?i)^data:image/(png|jpeg|webp|gif);base64,[A-Za-z0-9+/=\r\n]+$`)
)

func looksLikeSVG(data []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if start, ok := token.(xml.StartElement); ok {
			return strings.EqualFold(start.Name.Local, "svg")
		}
	}
}

// sanitizeSVG keeps a small presentation-only SVG subset. Scripts, foreign
// objects, event handlers, stylesheets, and external references are rejected.
func sanitizeSVG(data []byte) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	depth := 0
	rootSeen := false
	textDepth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			name := strings.ToLower(value.Name.Local)
			if !safeSVGElements[name] || (depth == 0 && (rootSeen || name != "svg")) {
				return nil, errors.New("unsupported SVG element")
			}
			rootSeen = true
			start := xml.StartElement{Name: xml.Name{Local: value.Name.Local}}
			for _, attr := range value.Attr {
				attrName := strings.ToLower(attr.Name.Local)
				if attr.Name.Space == "xmlns" || attrName == "xlink" || !safeSVGAttributes[attrName] {
					continue
				}
				if attrName == "href" && !localSVGRef.MatchString(attr.Value) && !dataImageURL.MatchString(attr.Value) {
					return nil, errors.New("external SVG references are not allowed")
				}
				if attrName == "href" && dataImageURL.MatchString(attr.Value) {
					if err := validateEmbeddedDataImage(attr.Value); err != nil {
						return nil, err
					}
				}
				for _, reference := range svgURL.FindAllString(attr.Value, -1) {
					if !localSVGURL.MatchString(reference) {
						return nil, errors.New("external SVG URLs are not allowed")
					}
				}
				if attrName == "xmlns" && attr.Value != "http://www.w3.org/2000/svg" {
					return nil, errors.New("invalid SVG namespace")
				}
				start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: attr.Name.Local}, Value: attr.Value})
			}
			if depth == 0 {
				foundNamespace := false
				for _, attr := range start.Attr {
					if attr.Name.Local == "xmlns" {
						foundNamespace = true
					}
				}
				if !foundNamespace {
					start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
				}
			}
			if name == "text" || name == "tspan" || name == "title" || name == "desc" {
				textDepth++
			}
			if err := encoder.EncodeToken(start); err != nil {
				return nil, err
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				return nil, errors.New("unbalanced SVG document")
			}
			name := strings.ToLower(value.Name.Local)
			if name == "text" || name == "tspan" || name == "title" || name == "desc" {
				textDepth--
			}
			depth--
			if err := encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: value.Name.Local}}); err != nil {
				return nil, err
			}
		case xml.CharData:
			if textDepth > 0 && strings.TrimSpace(string(value)) != "" {
				if err := encoder.EncodeToken(value); err != nil {
					return nil, err
				}
			}
		case xml.Directive:
			return nil, errors.New("SVG directives are not allowed")
		}
	}
	if !rootSeen || depth != 0 {
		return nil, errors.New("incomplete SVG document")
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
