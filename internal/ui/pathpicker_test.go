package ui

import "testing"

func TestParsePathQuery(t *testing.T) {
	home := "/home/u"
	tests := []struct {
		text, dir, filter string
		ok                bool
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
	}
	for _, test := range tests {
		dir, filter, ok := parsePathQuery(test.text, home)
		if ok != test.ok || dir != test.dir || filter != test.filter {
			t.Errorf("parsePathQuery(%q) = (%q, %q, %v), want (%q, %q, %v)",
				test.text, dir, filter, ok, test.dir, test.filter, test.ok)
		}
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
	}
	for _, test := range tests {
		got, ok := parentPathQuery(test.text, "/home/u")
		if ok != test.ok || got != test.want {
			t.Errorf("parentPathQuery(%q) = (%q, %v), want (%q, %v)", test.text, got, ok, test.want, test.ok)
		}
	}
}
