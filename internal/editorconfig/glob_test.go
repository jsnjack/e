package editorconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatches(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		rel     string
		want    bool
	}{
		{"star basename anywhere", "*.go", "a/b/main.go", true},
		{"star not across slash", "lib/*.js", "lib/a/b.js", false},
		{"double star across slash", "lib/**.js", "lib/a/b.js", true},
		{"slash double star slash zero dirs", "lib/**/b.js", "lib/b.js", true},
		{"slash pattern anchored", "lib/*.js", "src/lib/a.js", false},
		{"leading slash anchored", "/main.go", "main.go", true},
		{"leading slash not nested", "/main.go", "a/main.go", false},
		{"question mark", "?.go", "a.go", true},
		{"question mark not slash", "a?b", "a/b", false},
		{"class", "[ab].go", "b.go", true},
		{"negated class", "[!a].go", "a.go", false},
		{"negated class other", "[!a].go", "b.go", true},
		{"class range", "[a-c].go", "b.go", true},
		{"several brace groups", "{a,b}/{c,d}.go", "b/d.go", true},
		{"several brace groups miss", "{a,b}/{c,d}.go", "b/e.go", false},
		{"nested braces", "*.{j{s,sx},ts}", "app.jsx", true},
		{"single word brace literal", "{go}", "{go}", true},
		{"numeric range", "file{1..3}.txt", "file2.txt", true},
		{"numeric range outside", "file{1..3}.txt", "file4.txt", false},
		{"negative range", "n{-5..-1}", "n-3", true},
		{"escaped star", `a\*`, "a*", true},
		{"escaped star literal", `a\*`, "ab", false},
		{"unclosed brace literal", "a{b", "a{b", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matches(tc.pattern, tc.rel); got != tc.want {
				t.Fatalf("matches(%q, %q) = %v, want %v", tc.pattern, tc.rel, got, tc.want)
			}
		})
	}
}

func TestIndentSizeTabUsesFinalTabWidth(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".editorconfig"), []byte("root = true\n[*]\nindent_size = tab\ntab_width = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, ".editorconfig"), []byte("[*.go]\ntab_width = 8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	props, err := Resolve(filepath.Join(child, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if props.IndentSize != 8 || props.TabWidth != 8 {
		t.Fatalf("indent_size = %d, tab_width = %d, want 8 and 8", props.IndentSize, props.TabWidth)
	}
}
