package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	got := expandHome([]string{"~", "~/notes.md", "a~b", "/tmp/x", "~other"})
	want := []string{home, filepath.Join(home, "notes.md"), "a~b", "/tmp/x", "~other"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expandHome() = %v, want %v", got, want)
	}
}
