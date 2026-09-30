package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// batchReadTargets reads the targets of `batch submit` / `batch append`.
//
//   - a .json file: an array of item objects ({"url": ..., "external_id": ...,
//     <per-item overrides>}) or of URL strings
//   - a .jsonl file: one item object per line
//   - anything else: one URL per line (# comments and blank lines skipped)
//   - "-": stdin, sniffed: '[' is a JSON array, '{' is JSONL, else URL lines
//
// Exactly one of urls or items is non-empty on success.
func batchReadTargets(path string) (urls []string, items []map[string]any, err error) {
	var data []byte
	kind := ""
	if path == "-" {
		if data, err = readAllStdin(); err != nil {
			return nil, nil, Usagef("read stdin: %v", err)
		}
		switch t := bytes.TrimSpace(data); {
		case len(t) > 0 && t[0] == '[':
			kind = "json"
		case len(t) > 0 && t[0] == '{':
			kind = "jsonl"
		default:
			urls = batchSplitLines(data)
		}
	} else {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".json":
			kind = "json"
		case ".jsonl", ".ndjson":
			kind = "jsonl"
		}
		if kind != "" {
			if data, err = os.ReadFile(path); err != nil {
				return nil, nil, Usagef("read %s: %v", path, err)
			}
		} else if urls, err = readLines(path); err != nil {
			return nil, nil, Usagef("read %s: %v", path, err)
		}
	}

	switch kind {
	case "json":
		urls, items, err = batchParseJSONArray(data)
	case "jsonl":
		items, err = batchParseJSONL(data)
	}
	if err != nil {
		return nil, nil, err
	}
	for i, it := range items {
		if u, _ := it["url"].(string); strings.TrimSpace(u) == "" {
			return nil, nil, Usagef("item %d: missing \"url\"", i)
		}
	}
	if len(urls) == 0 && len(items) == 0 {
		return nil, nil, Usagef("%s: no targets (want one URL per line, a .json array or .jsonl items)", batchDisplayPath(path))
	}
	return urls, items, nil
}

func batchDisplayPath(path string) string {
	if path == "-" {
		return "stdin"
	}
	return path
}

// batchSplitLines mirrors readLines for bytes already in memory.
func batchSplitLines(b []byte) []string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// batchParseJSONArray accepts an array of URL strings (returned as urls) or
// of item objects / strings (returned as items).
func batchParseJSONArray(b []byte) ([]string, []map[string]any, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, nil, Usagef("items: want a JSON array of item objects: %v", err)
	}
	var urls []string
	allStrings := true
	for _, r := range raw {
		var s string
		if json.Unmarshal(r, &s) != nil {
			allStrings = false
			break
		}
		urls = append(urls, s)
	}
	if allStrings {
		return urls, nil, nil
	}
	items := make([]map[string]any, 0, len(raw))
	for i, r := range raw {
		var s string
		if json.Unmarshal(r, &s) == nil {
			items = append(items, map[string]any{"url": s})
			continue
		}
		it, err := batchDecodeItem(r)
		if err != nil {
			return nil, nil, Usagef("item %d: %v", i, err)
		}
		items = append(items, it)
	}
	return nil, items, nil
}

func batchParseJSONL(b []byte) ([]map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var items []map[string]any
	for i := 0; ; i++ {
		var it map[string]any
		err := dec.Decode(&it)
		if errors.Is(err, io.EOF) {
			return items, nil
		}
		if err != nil {
			return nil, Usagef("item %d: want one JSON object per line: %v", i, err)
		}
		if it == nil {
			return nil, Usagef("item %d: want a JSON object", i)
		}
		items = append(items, it)
	}
}

func batchDecodeItem(r json.RawMessage) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(r))
	dec.UseNumber()
	var it map[string]any
	if err := dec.Decode(&it); err != nil || it == nil {
		return nil, errors.New("want an object with a \"url\"")
	}
	return it, nil
}
