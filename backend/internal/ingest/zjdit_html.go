package ingest

import (
	"bytes"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
)

var zjditPageIdentity = regexp.MustCompile(`if\s*\(\s*href\s*==\s*['"]/Position/details\.html\?id=([0-9]+)['"]\s*\)`)

func zjditAttribute(node *html.Node, key string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val
		}
	}
	return ""
}

func zjditNodes(root *html.Node, tag, class string) []*html.Node {
	var nodes []*html.Node
	for node := range root.Descendants() {
		if node.Type == html.ElementNode && (tag == "" || node.Data == tag) && (class == "" || slices.Contains(strings.Fields(zjditAttribute(node, "class")), class)) {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func zjditNodeText(node *html.Node) (string, error) {
	var buffer bytes.Buffer
	if err := html.Render(&buffer, node); err != nil {
		return "", ErrSchema
	}
	return PlainText(buffer.String()), nil
}

func zjditSingleText(root *html.Node, tag, class string) (string, error) {
	nodes := zjditNodes(root, tag, class)
	if len(nodes) != 1 {
		return "", ErrSchema
	}
	return zjditNodeText(nodes[0])
}

func zjditEquivalent(a, b string) bool {
	return strings.Join(strings.Fields(a), "") == strings.Join(strings.Fields(b), "")
}

// zjditVerifyDetail confines all evidence to the main position panel; sidebar
// suggestions and generic login templates cannot stand in for the queried job.
func zjditVerifyDetail(body []byte, entry map[string]any) error {
	identities := zjditPageIdentity.FindAllSubmatch(body, -1)
	if len(identities) != 1 || string(identities[0][1]) != text(entry["id"]) {
		return ErrSchema
	}
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ErrSchema
	}
	panels := zjditNodes(root, "div", "post-details-panel")
	if len(panels) != 1 {
		return ErrSchema
	}
	panel := panels[0]
	title, err := zjditSingleText(panel, "h3", "title")
	if err != nil || !zjditEquivalent(title, PlainText(text(entry["name"]))) {
		return ErrSchema
	}
	info := zjditNodes(panel, "div", "info")
	if len(info) != 1 {
		return ErrSchema
	}
	var labels, other []string
	for _, span := range zjditNodes(info[0], "span", "") {
		value, err := zjditNodeText(span)
		if err != nil {
			return err
		}
		if slices.Contains(strings.Fields(zjditAttribute(span, "class")), "xzhao") {
			labels = append(labels, value)
		} else {
			other = append(other, value)
		}
	}
	want, err := zjditLabels(entry["labels"])
	if err != nil {
		return err
	}
	slices.Sort(labels)
	if !slices.Equal(labels, want) || len(other) != 2 || !zjditEquivalent(other[0], PlainText(text(entry["city"]))) || !zjditEquivalent(other[1], PlainText(text(entry["qualification"]))) {
		return ErrSchema
	}
	sections := zjditNodes(panel, "div", "panel-desc")
	if len(sections) != 1 {
		return ErrSchema
	}
	headings, contents := zjditNodes(sections[0], "h4", "dt"), zjditNodes(sections[0], "div", "dd")
	if len(headings) != 2 || len(contents) != 2 {
		return ErrSchema
	}
	for i, expected := range []struct{ heading, field string }{{heading: "岗位职责", field: "responsibility"}, {heading: "任职要求", field: "requirement"}} {
		heading, err := zjditNodeText(headings[i])
		if err != nil || heading != expected.heading {
			return ErrSchema
		}
		content, err := zjditNodeText(contents[i])
		if err != nil || content == "" || !zjditEquivalent(content, PlainText(text(entry[expected.field]))) {
			return ErrSchema
		}
	}
	buttons := zjditNodes(panel, "div", "panel-button")
	if len(buttons) != 1 {
		return ErrSchema
	}
	links := zjditNodes(buttons[0], "a", "")
	if len(links) != 1 || zjditAttribute(links[0], "href") != "https://zp.zjdit.com/login/index.html" {
		return ErrSchema
	}
	label, err := zjditNodeText(links[0])
	if err != nil || label != "登录投递" {
		return ErrSchema
	}
	return nil
}
