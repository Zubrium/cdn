package runtime

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"strings"

	htmlparser "golang.org/x/net/html"
	htmlatom "golang.org/x/net/html/atom"
)

// StaticHTMLFeature is a verified static HTML placement prepared by the
// snapshot service. Entry is relative to the feature ZIP root and Digest pins
// every referenced asset to the same immutable build.
type StaticHTMLFeature struct {
	FeatureKey  string
	PlacementID string
	Slot        string
	Digest      string
	Entry       string
	HTML        string
	StyleURLs   []string
}

// RenderStaticHTMLFallback puts the verified static fragment directly into
// its existing neutral placement root and includes immutable styles in the
// document head. The same roots are used with and without JavaScript.
func RenderStaticHTMLFallback(shell []byte, snapshotID string, contentRevision int64, pageKind string, sharedStyleURLs []string, features []StaticHTMLFeature) ([]byte, error) {
	if len(features) == 0 {
		return shell, nil
	}
	if !hasRenderedPlacementRoots(shell) {
		roots := make([]PlacementRoot, 0, len(features))
		for _, feature := range features {
			roots = append(roots, PlacementRoot{ID: feature.PlacementID, Slot: feature.Slot, FeatureKey: feature.FeatureKey})
		}
		var err error
		shell, err = RenderPlacementRoots(shell, pageKind, snapshotID, contentRevision, roots)
		if err != nil {
			return nil, err
		}
	}
	document, err := htmlparser.Parse(bytes.NewReader(shell))
	if err != nil {
		return nil, fmt.Errorf("parse public runtime shell: %w", err)
	}
	head, root, err := findDocumentSections(document)
	if err != nil {
		return nil, err
	}

	styleURLs := append([]string(nil), sharedStyleURLs...)
	for _, feature := range features {
		styleURLs = append(styleURLs, feature.StyleURLs...)
	}
	seenStyles := map[string]bool{}
	for _, href := range styleURLs {
		if href == "" || seenStyles[href] {
			continue
		}
		seenStyles[href] = true
		link := &htmlparser.Node{Type: htmlparser.ElementNode, Data: "link", DataAtom: htmlatom.Link}
		link.Attr = []htmlparser.Attribute{{Key: "rel", Val: "stylesheet"}, {Key: "href", Val: href}, {Key: "data-feature-style", Val: href}}
		head.AppendChild(link)
	}

	for _, feature := range features {
		placement := findPlacementRoot(root, feature.PlacementID)
		if placement == nil {
			return nil, fmt.Errorf("static HTML placement %q has no server-rendered root", feature.PlacementID)
		}
		if attrValue(placement, "data-engine-slot") != feature.Slot || attrValue(placement, "data-engine-feature") != feature.FeatureKey {
			return nil, fmt.Errorf("static HTML placement %q does not match its server-rendered root", feature.PlacementID)
		}
		for placement.FirstChild != nil {
			placement.RemoveChild(placement.FirstChild)
		}
		context := &htmlparser.Node{Type: htmlparser.ElementNode, Data: "div", DataAtom: htmlatom.Div}
		fragment, err := htmlparser.ParseFragment(strings.NewReader(feature.HTML), context)
		if err != nil {
			return nil, fmt.Errorf("parse static HTML for feature %q: %w", feature.FeatureKey, err)
		}
		for _, node := range fragment {
			if err := rewriteStaticFeatureResources(node, feature.Digest, feature.Entry); err != nil {
				return nil, fmt.Errorf("rewrite resources for feature %q: %w", feature.FeatureKey, err)
			}
			placement.AppendChild(node)
		}
	}

	var output bytes.Buffer
	if err := htmlparser.Render(&output, document); err != nil {
		return nil, fmt.Errorf("render static HTML fallback: %w", err)
	}
	return output.Bytes(), nil
}

func findDocumentSections(document *htmlparser.Node) (*htmlparser.Node, *htmlparser.Node, error) {
	var head, root *htmlparser.Node
	stack := []*htmlparser.Node{document}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node.Type == htmlparser.ElementNode {
			switch node.Data {
			case "head":
				head = node
			case "div":
				if attrValue(node, "id") == "root" {
					if root != nil {
						return nil, nil, fmt.Errorf("public runtime document has multiple #root elements")
					}
					root = node
				}
			}
		}
		for child := node.LastChild; child != nil; child = child.PrevSibling {
			stack = append(stack, child)
		}
	}
	if head == nil || root == nil {
		return nil, nil, fmt.Errorf("public runtime shell has no head or #root")
	}
	return head, root, nil
}

func hasRenderedPlacementRoots(document []byte) bool {
	parsed, err := htmlparser.Parse(bytes.NewReader(document))
	if err != nil {
		return false
	}
	_, root, err := findDocumentSections(parsed)
	if err != nil {
		return false
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == htmlparser.ElementNode {
			return true
		}
	}
	return false
}

func findPlacementRoot(root *htmlparser.Node, placementID string) *htmlparser.Node {
	stack := []*htmlparser.Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node.Type == htmlparser.ElementNode && attrValue(node, "data-engine-placement") == placementID {
			return node
		}
		for child := node.LastChild; child != nil; child = child.PrevSibling {
			stack = append(stack, child)
		}
	}
	return nil
}

func attrValue(node *htmlparser.Node, key string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val
		}
	}
	return ""
}

func rewriteStaticFeatureResources(root *htmlparser.Node, digest, entry string) error {
	stack := []*htmlparser.Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node.Type == htmlparser.ElementNode {
			for i := range node.Attr {
				switch strings.ToLower(node.Attr[i].Key) {
				case "src", "poster":
					resolved, err := staticFeatureResourceURL(digest, entry, node.Attr[i].Val)
					if err != nil {
						return err
					}
					node.Attr[i].Val = resolved
				case "srcset":
					resolved, err := staticFeatureSrcsetURL(digest, entry, node.Attr[i].Val)
					if err != nil {
						return err
					}
					node.Attr[i].Val = resolved
				}
			}
		}
		for child := node.LastChild; child != nil; child = child.PrevSibling {
			stack = append(stack, child)
		}
	}
	return nil
}

func staticFeatureResourceURL(digest, entry, reference string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(reference))
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || strings.HasPrefix(parsed.Path, "/") || strings.Contains(parsed.Path, "\\") {
		return "", fmt.Errorf("feature resource must be a relative ZIP path")
	}
	resolved := path.Clean(path.Join(path.Dir(entry), parsed.Path))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") || digest == "" {
		return "", fmt.Errorf("feature resource escapes its immutable build")
	}
	return (&url.URL{Path: "/feature-assets/" + digest + "/" + resolved}).EscapedPath(), nil
}

func staticFeatureSrcsetURL(digest, entry, source string) (string, error) {
	candidates := strings.Split(source, ",")
	for i, candidate := range candidates {
		fields := strings.Fields(strings.TrimSpace(candidate))
		if len(fields) == 0 || len(fields) > 2 {
			return "", fmt.Errorf("invalid srcset candidate")
		}
		resolved, err := staticFeatureResourceURL(digest, entry, fields[0])
		if err != nil {
			return "", err
		}
		if len(fields) == 2 {
			candidates[i] = resolved + " " + fields[1]
		} else {
			candidates[i] = resolved
		}
	}
	return strings.Join(candidates, ", "), nil
}
