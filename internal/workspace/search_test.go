package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueryOptions(t *testing.T) {
	for _, test := range []struct {
		query Query
		text  string
		want  []string
	}{
		{Query{Text: "foo"}, "Foo foo FOO", []string{"Foo", "foo", "FOO"}},
		{Query{Text: "foo", MatchCase: true}, "Foo foo FOO", []string{"foo"}},
		{Query{Text: "foo", WholeWord: true}, "foo food foo_ x.foo", []string{"foo", "foo"}},
		{Query{Text: "-flag", WholeWord: true}, "a -flag -flags", []string{"-flag"}},
		{Query{Text: "a.b"}, "a.b axb", []string{"a.b"}},
		{Query{Text: "a.b", Regex: true}, "a.b axb", []string{"a.b", "axb"}},
		{Query{Text: "^go", Regex: true}, "go\nno go\ngo", []string{"go", "go"}},
		{Query{Text: "a|b", Regex: true, WholeWord: true}, "a ab b", []string{"a", "b"}},
	} {
		re, err := test.query.Compile()
		if err != nil {
			t.Fatalf("%+v: %v", test.query, err)
		}
		if got := re.FindAllString(test.text, -1); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%+v in %q = %q, want %q", test.query, test.text, got, test.want)
		}
	}
	if _, err := (Query{Text: "(", Regex: true}).Compile(); err == nil {
		t.Error("an invalid expression compiled")
	}
	if _, err := (Query{Text: "("}).Compile(); err != nil {
		t.Errorf("a literal parenthesis did not compile: %v", err)
	}
}

func TestSearchPositionsAndContext(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"a.go":       "package a\n\n\tfunc héllo() { hello() }\r\n",
		"b/bom.txt":  "\xEF\xBB\xBFhello",
		"binary.bin": "hello\x00world",
		"none.txt":   "nothing here",
	})
	re, _ := Query{Text: "hello"}.Compile()
	result, err := Search(context.Background(), root, re, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{
		{Path: "a.go", Line: 2, Column: 16, EndLine: 2, EndColumn: 21, Before: "func héllo() { ", Text: "hello", After: "() }"},
		{Path: filepath.Join("b", "bom.txt"), Line: 0, Column: 0, EndLine: 0, EndColumn: 5, Text: "hello"},
	}
	if !reflect.DeepEqual(result.Matches, want) {
		t.Fatalf("matches = %+v\nwant %+v", result.Matches, want)
	}
	if result.Files != 2 || result.Truncated {
		t.Fatalf("files = %d, truncated = %v", result.Files, result.Truncated)
	}
}

func TestSearchSpansLinesAndCutsLongLines(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("x", 100) + "needle" + strings.Repeat("y", 300)
	writeFiles(t, root, map[string]string{"a.txt": "one\ntwo\n" + long})
	re, _ := Query{Text: `one\ntw`, Regex: true}.Compile()
	result, err := Search(context.Background(), root, re, 10, false)
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("matches = %+v, err = %v", result.Matches, err)
	}
	if m := result.Matches[0]; m.Line != 0 || m.Column != 0 || m.EndLine != 1 || m.EndColumn != 2 || m.Text != "one" {
		t.Fatalf("spanning match = %+v", m)
	}
	re, _ = Query{Text: "needle"}.Compile()
	result, _ = Search(context.Background(), root, re, 10, false)
	m := result.Matches[0]
	if m.Line != 2 || m.Column != 100 || !strings.HasPrefix(m.Before, "…") || !strings.HasSuffix(m.After, "…") {
		t.Fatalf("long-line match = %+v", m)
	}
	if len(m.Before) > maxContextBefore+len("…") || len(m.Text)+len(m.After) > maxContextAfter+len("…") {
		t.Fatalf("context was not cut short: %d + %d bytes", len(m.Before), len(m.After))
	}
}

func TestSearchStopsAtLimitInFileOrder(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{}
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		files[name] = "x x x"
	}
	writeFiles(t, root, files)
	re, _ := Query{Text: "x"}.Compile()
	result, err := Search(context.Background(), root, re, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 7 || !result.Truncated || result.Files != 3 {
		t.Fatalf("matches = %d, files = %d, truncated = %v", len(result.Matches), result.Files, result.Truncated)
	}
	for i, path := range []string{"a.txt", "a.txt", "a.txt", "b.txt", "b.txt", "b.txt", "c.txt"} {
		if result.Matches[i].Path != path {
			t.Fatalf("match %d is in %s, want %s", i, result.Matches[i].Path, path)
		}
	}
	result, _ = Search(context.Background(), root, re, 12, false)
	if len(result.Matches) != 12 || result.Truncated {
		t.Fatalf("an exact limit: matches = %d, truncated = %v", len(result.Matches), result.Truncated)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Search(ctx, root, re, 7, false); err == nil {
		t.Fatal("a cancelled search returned results")
	}
}
