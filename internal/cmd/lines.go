package cmd

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
)

// readLines returns the non-empty, non-comment lines of path ("-" = stdin).
// Used for URL lists by `scrape -` and `batch submit`.
func readLines(path string) ([]string, error) {
	if path == "-" {
		return scanLines(os.Stdin)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return scanLines(f)
}

func scanLines(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		// A UTF-8 BOM (Windows editors, `cat a b`) would end up in the URL.
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// readStdinLinesCtx is readLines("-") that returns ctx's error as soon as ctx
// is cancelled, so Ctrl-C works while stdin is idle. The reader goroutine is
// left blocked; the process is about to exit.
func readStdinLinesCtx(ctx context.Context) ([]string, error) {
	type result struct {
		lines []string
		err   error
	}
	in := os.Stdin
	ch := make(chan result, 1)
	go func() {
		lines, err := scanLines(in)
		ch <- result{lines, err}
	}()
	select {
	case r := <-ch:
		return r.lines, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
