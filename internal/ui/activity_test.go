package ui

import (
	"testing"
	"time"

	"github.com/jsnjack/e/internal/activity"
)

func TestActivityResultWording(t *testing.T) {
	tests := []struct {
		name  string
		event activity.Event
		want  string
	}{
		{"opened", activity.Event{Phase: activity.Finished, Label: "Opening go.sum"}, "Opened go.sum"},
		{"saved", activity.Event{Phase: activity.Finished, Label: "Saving main.go"}, "Saved main.go"},
		{"suggestion", activity.Event{Phase: activity.Finished, Label: "Generating suggestion"}, "Suggestion ready"},
		{"open failure", activity.Event{Phase: activity.Failed, Label: "Opening go.sum"}, "Couldn’t open go.sum"},
		{"AI failure", activity.Event{Phase: activity.Failed, Label: "Generating suggestion"}, "AI suggestion failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := activityResultLabel(test.event); got != test.want {
				t.Fatalf("label = %q, want %q", got, test.want)
			}
		})
	}
}

func TestActivityFormatting(t *testing.T) {
	event := activity.Event{Note: "cached", Model: "local-gemma", Fallback: true}
	if got := activityResultDetail(event); got != "cached · local-gemma" {
		t.Fatalf("detail = %q", got)
	}
	if got := activityLogDetail(event); got != "cached · local-gemma (fallback)" {
		t.Fatalf("log detail = %q", got)
	}
	if got := humanByteCount(5 * 1024 * 1024); got != "5.0 MB" {
		t.Fatalf("size = %q", got)
	}
	if got := shortDuration(1250 * time.Millisecond); got != "1.2s" {
		t.Fatalf("duration = %q", got)
	}
}

func TestStatusLanguage(t *testing.T) {
	for language, want := range map[string]string{
		"": "Plain Text", "go": "Go", "markdown": "Markdown",
		"shellscript": "Shell", "custom": "custom",
	} {
		if got := statusLanguage(language); got != want {
			t.Fatalf("statusLanguage(%q) = %q, want %q", language, got, want)
		}
	}
}
