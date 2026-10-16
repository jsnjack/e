package ui

import "testing"

func TestParsePathQuery(t *testing.T) {
	home, base := "/home/u", "/home/u/proj"
	tests := []struct {
		text, dir, filter string
		relative, ok      bool
	}{
		{text: "", ok: false},
		{text: "notes", ok: false},
		{text: "/", dir: "/", ok: true},
		{text: "/et", dir: "/", filter: "et", ok: true},
		{text: "/etc/", dir: "/etc", ok: true},
		{text: "~", dir: home, ok: true},
		{text: "~/", dir: home, ok: true},
		{text: "~/wo", dir: home, filter: "wo", ok: true},
		{text: "~/work/e/.git", dir: "/home/u/work/e", filter: ".git", ok: true},
		{text: "Downloads/", dir: "/home/u/proj/Downloads", relative: true, ok: true},
		{text: "src/ui/pal", dir: "/home/u/proj/src/ui", filter: "pal", relative: true, ok: true},
		{text: "./", dir: base, relative: true, ok: true},
		{text: "../", dir: home, relative: true, ok: true},
	}
	for _, test := range tests {
		got, ok := parsePathQuery(test.text, home, base)
		want := pathQuery{dir: test.dir, filter: test.filter, relative: test.relative}
		if ok != test.ok || (ok && got != want) {
			t.Errorf("parsePathQuery(%q) = (%+v, %v), want (%+v, %v)", test.text, got, ok, want, test.ok)
		}
	}
	if _, ok := parsePathQuery("src/x", home, ""); ok {
		t.Error("relative text without a base was read as a path")
	}
}

func TestParentPathQuery(t *testing.T) {
	tests := []struct {
		text, want string
		ok         bool
	}{
		{text: "/", ok: false},
		{text: "/etc", ok: false},
		{text: "/etc/", want: "/", ok: true},
		{text: "/usr/share/", want: "/usr/", ok: true},
		{text: "recent", ok: false},
		{text: "src/ui/", want: "src/", ok: true},
		{text: "src/", want: "", ok: true},
	}
	for _, test := range tests {
		got, ok := parentPathQuery(test.text, "/home/u", "/srv/proj")
		if ok != test.ok || got != test.want {
			t.Errorf("parentPathQuery(%q) = (%q, %v), want (%q, %v)", test.text, got, ok, test.want, test.ok)
		}
	}
}
