package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// decode parses YAML into a generic map for assertions.
func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestMigrateSample(t *testing.T) {
	src, err := os.ReadFile("sample-v40-values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out, warnings, err := migrate(src)
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)

	if _, ok := m["logs"]; ok {
		t.Error("logs should be removed")
	}
	log, ok := m["log"].(map[string]any)
	if !ok {
		t.Fatal("log should be a mapping")
	}
	if log["level"] != "DEBUG" {
		t.Errorf("log.level = %v, want DEBUG", log["level"])
	}
	al, ok := m["accessLog"].(map[string]any)
	if !ok {
		t.Fatal("accessLog should be a mapping")
	}

	filters := al["filters"].(map[string]any)
	for _, old := range []string{"statuscodes", "retryattempts", "minduration"} {
		if _, ok := filters[old]; ok {
			t.Errorf("accessLog.filters.%s should be renamed", old)
		}
	}
	if filters["statusCodes"] != "200,300-302" {
		t.Errorf("statusCodes = %v", filters["statusCodes"])
	}

	fields := al["fields"].(map[string]any)
	if _, ok := fields["general"]; ok {
		t.Error("accessLog.fields.general should be collapsed")
	}
	if fields["defaultMode"] != "keep" {
		t.Errorf("fields.defaultMode = %v, want keep", fields["defaultMode"])
	}
	if _, ok := fields["names"]; !ok {
		t.Error("fields.names should be moved up from general")
	}
	if fields["headers"].(map[string]any)["defaultMode"] != "drop" {
		t.Error("fields.headers.defaultMode missing")
	}
	if fields["queryParameters"].(map[string]any)["defaultMode"] != "drop" {
		t.Error("fields.queryParameters.defaultMode missing")
	}

	// unrelated keys pass through
	if m["service"].(map[string]any)["type"] != "LoadBalancer" {
		t.Error("service.type should pass through untouched")
	}

	// providers.file.content (YAML string) is converted to an object, no warning
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	content, ok := m["providers"].(map[string]any)["file"].(map[string]any)["content"].(map[string]any)
	if !ok {
		t.Fatal("providers.file.content should be converted to an object")
	}
	rule := content["http"].(map[string]any)["routers"].(map[string]any)["my-router"].(map[string]any)["rule"]
	if rule != "Host(`example.com`)" {
		t.Errorf("file.content not inlined correctly: rule=%v", rule)
	}
}

func TestConvertsFileContentString(t *testing.T) {
	in := []byte("providers:\n  file:\n    content: |\n      http:\n        routers:\n          r:\n            rule: \"Host(`x`)\"\n")
	out, warnings, err := migrate(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	m := decode(t, out)
	c, ok := m["providers"].(map[string]any)["file"].(map[string]any)["content"].(map[string]any)
	if !ok {
		t.Fatalf("content should be an object, got: %v", m["providers"])
	}
	if _, ok := c["http"]; !ok {
		t.Error("expected http key in the converted content object")
	}
}

func TestWarnsOnUnconvertibleContent(t *testing.T) {
	// a plain scalar string is valid YAML but not a mapping -> warn, leave as-is
	in := []byte("providers:\n  file:\n    content: \"just a string\"\n")
	out, warnings, err := migrate(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "doesn't parse to a YAML object") {
		t.Errorf("expected a parse warning, got %v", warnings)
	}
	if !strings.Contains(string(out), "just a string") {
		t.Error("unconvertible content should be left unchanged")
	}
}

func TestIdempotent(t *testing.T) {
	src, err := os.ReadFile("sample-v40-values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	once, _, err := migrate(src)
	if err != nil {
		t.Fatal(err)
	}
	twice, _, err := migrate(once)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("migrate is not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
}

func TestPreservesComments(t *testing.T) {
	in := []byte("# top comment\nlogs:\n  general:\n    level: DEBUG # inline\nservice:\n  type: LoadBalancer\n")
	out, _, err := migrate(in)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "# top comment") {
		t.Error("head comment lost")
	}
	if !strings.Contains(s, "# inline") {
		t.Error("inline comment on a moved key lost")
	}
}

func TestNoWarningWhenFileContentIsObject(t *testing.T) {
	in := []byte("providers:\n  file:\n    content:\n      http:\n        routers: {}\n")
	_, warnings, err := migrate(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("object file.content should not warn, got %v", warnings)
	}
}
