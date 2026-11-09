package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	htmlparser "golang.org/x/net/html"
)

func TestRenderPlacementRootsKeepsOneMainAndSiblingMountRoots(t *testing.T) {
	shell := []byte(`<!doctype html><html><head></head><body><div id="root"></div></body></html>`)
	rendered, err := RenderPlacementRoots(shell, "page", "snapshot-1", 7, []PlacementRoot{
		{ID: "footer", Slot: "footer", FeatureKey: "footer"},
		{ID: "main-1", Slot: "main", FeatureKey: "catalog"},
		{ID: "background", Slot: "background", FeatureKey: "background"},
		{ID: "main-2", Slot: "main", FeatureKey: "hero"},
		{ID: "header", Slot: "header", FeatureKey: "header"},
	})
	if err != nil {
		t.Fatal(err)
	}
	document, err := htmlparser.Parse(strings.NewReader(string(rendered)))
	if err != nil {
		t.Fatal(err)
	}
	root := findTestElementByID(document, "root")
	if root == nil {
		t.Fatal("server document does not contain #root")
	}
	if countTestElements(document, "main") != 1 {
		t.Fatalf("expected exactly one page-level main: %s", rendered)
	}
	main := findTestElement(root, "main")
	if main == nil || countDirectPlacementRoots(main) != 2 {
		t.Fatalf("main placements must be direct children of the one page main: %s", rendered)
	}
	if countDirectPlacementRoots(root) != 3 {
		t.Fatalf("background/header/footer roots must be direct children of #root: %s", rendered)
	}
	if findNestedPlacementRoot(root) {
		t.Fatalf("placement roots must not be nested: %s", rendered)
	}
	if attrValue(root, "data-snapshot") != "snapshot-1" || attrValue(root, "data-content-revision") != "7" {
		t.Fatalf("snapshot identity missing from root: %s", rendered)
	}
}

func TestRenderPlacementRootsEscapesAttributesAndRejectsInvalidShell(t *testing.T) {
	shell := []byte(`<!doctype html><html><head></head><body><div id="root"></div></body></html>`)
	rendered, err := RenderPlacementRoots(shell, `page"<`, `snap&1`, 1, []PlacementRoot{{ID: `x" onmouseover="bad`, Slot: "main", FeatureKey: `f<&`}})
	if err != nil {
		t.Fatal(err)
	}
	value := string(rendered)
	if strings.Contains(value, `onmouseover="bad`) || !strings.Contains(value, `data-engine-feature="f&lt;&amp;"`) {
		t.Fatalf("placement fields were not safely escaped: %s", value)
	}
	if _, err := RenderPlacementRoots([]byte(`<html><body></body></html>`), "page", "snap", 1, nil); err == nil {
		t.Fatal("expected shell without #root to be rejected")
	}
	if _, err := RenderPlacementRoots([]byte(`<html><body><div id="root"></div><div id="root"></div></body></html>`), "page", "snap", 1, nil); err == nil {
		t.Fatal("expected shell with multiple #root elements to be rejected")
	}
	if _, err := RenderPlacementRoots(shell, "page", "snap", 1, []PlacementRoot{{ID: "x", Slot: "aside", FeatureKey: "f"}}); err == nil {
		t.Fatal("expected unsupported placement slot to be rejected")
	}
}

func TestAppendRuntimeBootstrapPreservesJSONAndEscapesScriptEndTag(t *testing.T) {
	shell := []byte(`<!doctype html><html><head></head><body><div id="root"></div></body></html>`)
	payload := []byte(`{"title":"</script><script>attack()</script>","value":"<&>"}`)
	rendered, err := AppendRuntimeBootstrap(shell, payload)
	if err != nil {
		t.Fatal(err)
	}
	document, err := htmlparser.Parse(strings.NewReader(string(rendered)))
	if err != nil {
		t.Fatal(err)
	}
	if countTestElements(document, "script") != 1 {
		t.Fatalf("bootstrap value must not create a script element: %s", rendered)
	}
	bootstrap := findTestElementByID(document, "engine-runtime-bootstrap")
	if bootstrap == nil || attrValue(bootstrap, "type") != "application/json" {
		t.Fatalf("expected an inert JSON bootstrap: %s", rendered)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(childText(bootstrap)), &decoded); err != nil {
		t.Fatalf("bootstrap must remain valid JSON after HTML parsing: %v; document=%s", err, rendered)
	}
	if decoded["title"] != `</script><script>attack()</script>` || decoded["value"] != "<&>" {
		t.Fatalf("bootstrap values changed during escaping: %#v", decoded)
	}
	if strings.Count(string(rendered), "</script>") != 1 {
		t.Fatalf("only the trusted closing tag may remain in the document: %s", rendered)
	}
}

func TestAppendRuntimeBootstrapRejectsInvalidJSONOrMissingBody(t *testing.T) {
	if _, err := AppendRuntimeBootstrap([]byte(`<html><body></body></html>`), []byte(`{"ok":true} trailing`)); err == nil {
		t.Fatal("expected trailing invalid JSON to be rejected")
	}
	if _, err := AppendRuntimeBootstrap([]byte(`<html><head></head></html>`), []byte(`{"ok":true}`)); err == nil {
		t.Fatal("expected a document without body to be rejected")
	}
}

func findTestElement(node *htmlparser.Node, tag string) *htmlparser.Node {
	if node.Type == htmlparser.ElementNode && node.Data == tag {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if match := findTestElement(child, tag); match != nil {
			return match
		}
	}
	return nil
}

func findTestElementByID(node *htmlparser.Node, id string) *htmlparser.Node {
	if node.Type == htmlparser.ElementNode && attrValue(node, "id") == id {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if match := findTestElementByID(child, id); match != nil {
			return match
		}
	}
	return nil
}

func countTestElements(node *htmlparser.Node, tag string) int {
	count := 0
	if node.Type == htmlparser.ElementNode && node.Data == tag {
		count++
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		count += countTestElements(child, tag)
	}
	return count
}

func countDirectPlacementRoots(parent *htmlparser.Node) int {
	count := 0
	for child := parent.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == htmlparser.ElementNode && attrValue(child, "data-engine-placement") != "" {
			count++
		}
	}
	return count
}

func findNestedPlacementRoot(node *htmlparser.Node) bool {
	if node.Type == htmlparser.ElementNode && attrValue(node, "data-engine-placement") != "" {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if hasPlacementRoot(child) {
				return true
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if findNestedPlacementRoot(child) {
			return true
		}
	}
	return false
}

func hasPlacementRoot(node *htmlparser.Node) bool {
	if node.Type == htmlparser.ElementNode && attrValue(node, "data-engine-placement") != "" {
		return true
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if hasPlacementRoot(child) {
			return true
		}
	}
	return false
}

func childText(node *htmlparser.Node) string {
	var value strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == htmlparser.TextNode {
			value.WriteString(child.Data)
		}
	}
	return value.String()
}
