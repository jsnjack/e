package editorconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		body string
		file string
		want Properties
	}{
		{"go section", "root=true\n[*]\nindent_style=space\nindent_size=2\n[*.go]\nindent_size=4\ninsert_final_newline=true\n", "main.go", Properties{IndentStyle: "space", IndentSize: 4, TabWidth: 4, InsertFinalNewline: boolPtr(true)}},
		{"brace expansion", "[*.{js,ts}]\nindent_style=tab\ntab_width=8\n", "main.ts", Properties{IndentStyle: "tab", IndentSize: 8, TabWidth: 8}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".editorconfig"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Resolve(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if got.IndentStyle != tc.want.IndentStyle || got.IndentSize != tc.want.IndentSize || got.TabWidth != tc.want.TabWidth {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if tc.want.InsertFinalNewline != nil && (got.InsertFinalNewline == nil || *got.InsertFinalNewline != *tc.want.InsertFinalNewline) {
				t.Fatalf("insert_final_newline = %v", got.InsertFinalNewline)
			}
		})
	}
}

func boolPtr(value bool) *bool { return &value }
