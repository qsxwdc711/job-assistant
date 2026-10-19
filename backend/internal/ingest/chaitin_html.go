package ingest

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

type chaitinBrand struct{ Email, Subject string }

var chaitinPageIdentity = regexp.MustCompile(`const\s+jobID\s*=\s*localDebugConfig\.enabled\s*\?\s*detailPageQuery\.get\(['"]job_id['"]\)\s*\|\|\s*['"]([a-f0-9-]+)['"]\s*:\s*['"]([a-f0-9-]+)['"]`)

// chaitinBrandFromPage reads only the public application's JSON configuration.
// Embedded development URLs and JavaScript are never evaluated or requested.
func chaitinBrandFromPage(body []byte) (chaitinBrand, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return chaitinBrand{}, ErrSchema
	}
	var config []byte
	found := 0
	for node := range root.Descendants() {
		if node.Type != html.ElementNode || node.Data != "script" {
			continue
		}
		for _, attr := range node.Attr {
			if attr.Key == "id" && attr.Val == "career-site-brand-config" {
				found++
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					if child.Type == html.TextNode {
						config = append(config, child.Data...)
					}
				}
			}
		}
	}
	var value struct {
		Email   string `json:"resume_email"`
		Subject string `json:"mail_subject_template"`
	}
	if found != 1 || json.Unmarshal(config, &value) != nil || value.Email != "resume@chaitin.com" || len(value.Subject) > 300 || !strings.Contains(value.Subject, "{{job_title}}") || !strings.Contains(value.Subject, "{{tracking_code}}") {
		return chaitinBrand{}, ErrSchema
	}
	return chaitinBrand{Email: value.Email, Subject: value.Subject}, nil
}

func chaitinVerifyPage(body []byte, id string, brand chaitinBrand) error {
	matches := chaitinPageIdentity.FindAllSubmatch(body, -1)
	if len(matches) != 1 || string(matches[0][1]) != id || string(matches[0][2]) != id {
		return ErrSchema
	}
	actual, err := chaitinBrandFromPage(body)
	if err != nil || actual != brand {
		return ErrSchema
	}
	return nil
}
