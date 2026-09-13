package activity

import (
	"context"
	"errors"
	"testing"
)

func TestOperationLifecycle(t *testing.T) {
	hub := NewHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	op := hub.Begin(AI, "Generating suggestion")
	op.Progress(5, 10)
	op.Finish("Suggestion ready", "local-gemma", true)
	op.Fail(errors.New("late failure"))

	want := []Phase{Started, Progressed, Finished}
	for _, phase := range want {
		event := <-events
		if event.Phase != phase {
			t.Fatalf("phase = %v, want %v", event.Phase, phase)
		}
		if event.ID == 0 || event.Label != "Generating suggestion" {
			t.Fatalf("unexpected event: %#v", event)
		}
		if phase == Finished && (event.Model != "local-gemma" || !event.Fallback) {
			t.Fatalf("AI result metadata = %#v", event)
		}
	}
	select {
	case event := <-events:
		t.Fatalf("operation published twice: %#v", event)
	default:
	}
}

func TestCancellationAndProgressBounds(t *testing.T) {
	hub := NewHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	op := hub.Begin(File, "Opening file")
	<-events
	op.Progress(20, 10)
	progress := <-events
	if progress.Current != 10 || progress.Total != 10 {
		t.Fatalf("bounded progress = %d/%d", progress.Current, progress.Total)
	}
	op.Fail(context.Canceled)
	if event := <-events; event.Phase != Cancelled {
		t.Fatalf("cancel phase = %v", event.Phase)
	}
}

func TestPlural(t *testing.T) {
	if got := Plural(1, "file", "files"); got != "1 file" {
		t.Fatalf("singular = %q", got)
	}
	if got := Plural(2, "file", "files"); got != "2 files" {
		t.Fatalf("plural = %q", got)
	}
}
