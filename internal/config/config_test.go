package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMalformedIsFileError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvConfig, p)
	_, err := Load()
	var fe *FileError
	if !errors.As(err, &fe) || fe.Path != p || !strings.Contains(err.Error(), p) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Resolve("k", ""); !errors.As(err, &fe) {
		t.Errorf("Resolve err = %v", err)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	t.Setenv(EnvConfig, filepath.Join(t.TempDir(), "none.json"))
	if f, err := Load(); err != nil || f != (File{}) {
		t.Errorf("f=%+v err=%v", f, err)
	}
}
