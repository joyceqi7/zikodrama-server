package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fileFixture struct {
	Service struct {
		Name string `json:"name"`
	} `json:"service"`
}

func TestLoadFileScansAndClosesLoader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("service:\n  name: tiptop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var target fileFixture
	if err := LoadFile(path, &target); err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if target.Service.Name != "tiptop" {
		t.Fatalf("service.name = %q, want tiptop", target.Service.Name)
	}
}

func TestLoadFileRejectsInvalidInputs(t *testing.T) {
	var nilTarget *fileFixture
	for _, test := range []struct {
		name   string
		path   string
		target any
		want   string
	}{
		{name: "blank path", target: &fileFixture{}, want: "file path is required"},
		{name: "value target", path: "config.yaml", target: fileFixture{}, want: "non-nil pointer"},
		{name: "nil pointer", path: "config.yaml", target: nilTarget, want: "non-nil pointer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := LoadFile(test.path, test.target)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadFile() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestMustLoadFilePanicsOnFailure(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustLoadFile() did not panic")
		}
	}()
	MustLoadFile(filepath.Join(t.TempDir(), "missing.yaml"), &fileFixture{})
}
