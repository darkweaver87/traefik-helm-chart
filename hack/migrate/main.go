// Command migrate rewrites a Traefik Helm chart values override from chart v40
// to v41 — the #1887 logging key renames (logs.* -> log.* / accessLog.*, with
// camelCased sub-keys).
//
// Pure Go: no yq, no Docker, no OS-specific launcher. `go run` it, or ship a
// prebuilt binary. Dry-run by default (prints to stdout); -i rewrites in place
// with a .bak backup. Idempotent, and comments/key order are preserved.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func main() {
	inPlace := flag.Bool("i", false, "rewrite the file in place (backup written to <file>.bak)")
	flag.BoolVar(inPlace, "in-place", false, "alias for -i")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [-i] <values.yaml>\n\nMigrate a Traefik Helm chart values override from v40 to v41.\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	path := flag.Arg(0)

	src, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}

	out, warnings, err := migrate(src)
	if err != nil {
		fail(err)
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "WARNING: "+w)
	}

	if *inPlace {
		if err := os.WriteFile(path+".bak", src, 0o644); err != nil {
			fail(err)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			fail(err)
		}
		fmt.Fprintf(os.Stderr, "migrated in place: %s (backup: %s.bak)\n", path, path)
		return
	}
	_, _ = os.Stdout.Write(out)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// migrate applies the v40->v41 transforms to a values document and returns the
// rewritten YAML plus any non-fatal warnings.
func migrate(src []byte) ([]byte, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 { // empty file
		return src, nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("expected a YAML mapping at the top level")
	}

	migrateLogs(root)
	warnings := migrateFileContent(root)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, nil, err
	}
	_ = enc.Close()

	return buf.Bytes(), warnings, nil
}

// migrateLogs renames logs.general -> log and logs.access -> accessLog (in
// place, so the value node keeps its children and comments), then camelCases
// the accessLog sub-keys. Safe to run on an already-migrated file (no-op).
func migrateLogs(root *yaml.Node) {
	if logs := mapGet(root, "logs"); isMap(logs) {
		general := mapGet(logs, "general")
		access := mapGet(logs, "access")
		// Drop any partial pre-existing targets so we never duplicate a key.
		mapDelete(root, "log")
		mapDelete(root, "accessLog")
		logsKey := root.Content[mapIndex(root, "logs")] // carries comments on `logs:`
		var pairs []*yaml.Node
		if general != nil {
			pairs = append(pairs, scalar("log"), general)
		}
		if access != nil {
			pairs = append(pairs, scalar("accessLog"), access)
		}
		if len(pairs) > 0 { // keep comments that sat on the `logs:` key
			pairs[0].HeadComment = logsKey.HeadComment
			pairs[0].LineComment = logsKey.LineComment
			pairs[0].FootComment = logsKey.FootComment
		}
		replaceKey(root, "logs", pairs)
	}

	al := mapGet(root, "accessLog")
	if !isMap(al) {
		return
	}
	if filters := mapGet(al, "filters"); isMap(filters) {
		mapRename(filters, "statuscodes", "statusCodes")
		mapRename(filters, "retryattempts", "retryAttempts")
		mapRename(filters, "minduration", "minDuration")
	}
	if fields := mapGet(al, "fields"); isMap(fields) {
		// fields.general.* collapses up one level into fields.
		if gen := mapGet(fields, "general"); isMap(gen) {
			if dm := mapGet(gen, "defaultmode"); dm != nil {
				mapSet(fields, "defaultMode", dm)
			}
			if names := mapGet(gen, "names"); names != nil {
				mapSet(fields, "names", names)
			}
			mapDelete(fields, "general")
		}
		if h := mapGet(fields, "headers"); isMap(h) {
			mapRename(h, "defaultmode", "defaultMode")
		}
		if q := mapGet(fields, "queryParameters"); isMap(q) {
			mapRename(q, "defaultmode", "defaultMode")
		}
	}
}

// migrateFileContent converts providers.file.content from a v40 YAML *string*
// into the v41 *object* by parsing the embedded YAML and inlining it (comments
// preserved). Already-object content is left untouched; content that doesn't
// parse to a YAML mapping is left as-is with a warning.
func migrateFileContent(root *yaml.Node) []string {
	providers := mapGet(root, "providers")
	if !isMap(providers) {
		return nil
	}
	file := mapGet(providers, "file")
	if !isMap(file) {
		return nil
	}
	i := mapIndex(file, "content")
	if i < 0 {
		return nil
	}
	c := file.Content[i+1]
	if c.Kind != yaml.ScalarNode || c.Tag == "!!null" {
		return nil // already an object (or null) — nothing to do
	}

	var parsed yaml.Node
	if err := yaml.Unmarshal([]byte(c.Value), &parsed); err != nil || len(parsed.Content) == 0 || parsed.Content[0].Kind != yaml.MappingNode {
		return []string{"providers.file.content is a string that doesn't parse to a YAML object; " +
			"migrate it by hand. See traefik-helm-chart#1887."}
	}
	inlined := parsed.Content[0]
	inlined.HeadComment = c.HeadComment // keep comments that sat on `content:`
	inlined.LineComment = c.LineComment
	inlined.FootComment = c.FootComment
	file.Content[i+1] = inlined
	return nil
}

// --- minimal yaml.Node mapping helpers ------------------------------------

func isMap(n *yaml.Node) bool { return n != nil && n.Kind == yaml.MappingNode }

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// mapIndex returns the index of key's key-node in m.Content (-1 if absent).
func mapIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if i := mapIndex(m, key); i >= 0 {
		return m.Content[i+1]
	}
	return nil
}

func mapDelete(m *yaml.Node, key string) {
	if i := mapIndex(m, key); i >= 0 {
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
	}
}

// mapRename changes a key's name in place, keeping its value node and comments.
func mapRename(m *yaml.Node, old, new string) {
	if i := mapIndex(m, old); i >= 0 {
		m.Content[i].Value = new
	}
}

// mapSet sets key=val, replacing an existing entry or appending a new one.
func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	if i := mapIndex(m, key); i >= 0 {
		m.Content[i+1] = val
		return
	}
	m.Content = append(m.Content, scalar(key), val)
}

// replaceKey swaps the key/value pair for `key` with pairs, at the same position.
func replaceKey(m *yaml.Node, key string, pairs []*yaml.Node) {
	i := mapIndex(m, key)
	if i < 0 {
		return
	}
	tail := append([]*yaml.Node{}, m.Content[i+2:]...)
	m.Content = append(m.Content[:i], pairs...)
	m.Content = append(m.Content, tail...)
}
