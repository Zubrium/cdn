package media

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// ValidateSVGReader accepts only presentation-only SVG without rewriting
// bytes. It is used when immutable uploaded originals must keep their digest.
func ValidateSVGReader(reader io.Reader) error {
	decoder := xml.NewDecoder(reader)
	depth := 0
	root := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch v := token.(type) {
		case xml.StartElement:
			name := strings.ToLower(v.Name.Local)
			if !safeSVGElements[name] || (depth == 0 && (root || name != "svg")) {
				return fmt.Errorf("unsafe SVG element")
			}
			root = true
			depth++
			for _, attr := range v.Attr {
				name := strings.ToLower(attr.Name.Local)
				if attr.Name.Space == "xmlns" {
					if attr.Value != "http://www.w3.org/1999/xlink" {
						return fmt.Errorf("unsafe SVG namespace")
					}
					continue
				}
				if !safeSVGAttributes[name] {
					return fmt.Errorf("unsafe SVG attribute")
				}
				if name == "href" && !localSVGRef.MatchString(attr.Value) && !dataImageURL.MatchString(attr.Value) {
					return fmt.Errorf("unsafe SVG reference")
				}
				if name == "href" && dataImageURL.MatchString(attr.Value) {
					if err := validateEmbeddedDataImage(attr.Value); err != nil {
						return err
					}
				}
				for _, reference := range svgURL.FindAllString(attr.Value, -1) {
					if !localSVGURL.MatchString(reference) {
						return fmt.Errorf("unsafe SVG URL")
					}
				}
				if name == "xmlns" && attr.Value != "http://www.w3.org/2000/svg" {
					return fmt.Errorf("unsafe SVG namespace")
				}
			}
		case xml.EndElement:
			depth--
			if depth < 0 {
				return fmt.Errorf("unbalanced SVG")
			}
		case xml.Directive:
			return fmt.Errorf("SVG directives are not allowed")
		case xml.ProcInst:
			if v.Target != "xml" {
				return fmt.Errorf("SVG instructions are not allowed")
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(v)) != "" {
				return fmt.Errorf("invalid SVG content")
			}
		}
	}
	if !root || depth != 0 {
		return fmt.Errorf("incomplete SVG")
	}
	return nil
}

func validateEmbeddedDataImage(value string) error {
	parts := strings.SplitN(value, ",", 2)
	if len(parts) != 2 || !strings.HasPrefix(strings.ToLower(parts[0]), "data:image/") {
		return fmt.Errorf("invalid SVG data image")
	}
	mediaType := strings.ToLower(strings.TrimSuffix(parts[0][len("data:"):], ";base64"))
	if mediaType == "image/gif" {
		return fmt.Errorf("animated SVG raster references are not supported")
	}
	encoded := strings.Map(func(character rune) rune {
		if character == '\r' || character == '\n' {
			return -1
		}
		return character
	}, parts[1])
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("invalid SVG data image: %w", err)
	}
	if mediaType == "image/webp" && isAnimatedWebP(data) {
		return fmt.Errorf("animated SVG raster references are not supported")
	}
	if err := ValidateRasterReader(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("unsupported embedded SVG raster: %w", err)
	}
	return nil
}
