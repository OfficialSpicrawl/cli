// Package schema embeds the JSON Schemas of the API's request bodies so
// `spicrawl schema` can print them offline.
//
// The *.json files are generated from openapi.yaml with every $ref
// inlined (recursive definitions live under $defs), so each file is
// self-contained. Regenerate after changing the spec:
//
//	go generate ./internal/schema/
package schema

//go:generate python3 gen.py

import (
	"embed"
	"sort"
	"strings"
)

//go:embed *.json
var files embed.FS

// Names lists the available schemas, sorted.
func Names() []string {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// Get returns the schema called name ("scrape", "batch", ...).
func Get(name string) ([]byte, bool) {
	if name == "" || strings.ContainsAny(name, "/\\.") {
		return nil, false
	}
	b, err := files.ReadFile(name + ".json")
	if err != nil {
		return nil, false
	}
	return b, true
}
