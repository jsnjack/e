package ui

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/activity"
	"github.com/jsnjack/e/internal/ai"
	"github.com/jsnjack/e/internal/dispatch"
)

func (w *window) showAITransform() {
	doc := w.active()
	if doc == nil || w.completer == nil {
		w.toast("Configure an AI model first")
		return
	}
	start, end, ok := doc.buffer.SelectionBounds()
	if !ok || start.Offset() == end.Offset() {
		w.toast("Select text to transform")
		return
	}
	startOffset, endOffset := start.Offset(), end.Offset()
	selected := doc.buffer.Text(start, end, true)
	if len(selected) > 64*1024 {
		w.toast("Select at most 64 KB for an AI transformation")
		return
	}
	dialog := adw.NewAlertDialog("Transform Selection", "Describe the result you want. E will show the replacement before changing your document.")
	instruction := gtk.NewEntry()
	instruction.SetPlaceholderText("For example: simplify this and preserve behavior")
	instruction.SetActivatesDefault(true)
	dialog.SetExtraChild(instruction)
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("transform", "Transform")
	dialog.SetCloseResponse("cancel")
	dialog.SetDefaultResponse("transform")
	dialog.SetResponseAppearance("transform", adw.ResponseSuggested)
	dialog.ConnectResponse(func(response string) {
		prompt := strings.TrimSpace(instruction.Text())
		if response != "transform" || prompt == "" {
			return
		}
		revision := doc.revision
		operation := w.activityHub.Begin(activity.AI, "Transforming selection")
		completer := w.completer
		go func() {
			result, err := completer.CompleteDetailed(w.ctx, ai.Request{
				Filename: doc.path, Language: languageForPath(doc.path),
				Instruction: prompt, Selection: selected,
			})
			dispatch.Main(func() {
				if err != nil {
					operation.Fail(err)
					return
				}
				if strings.TrimSpace(result.Text) == "" {
					operation.Fail(fmt.Errorf("the model returned an empty replacement"))
					return
				}
				if doc.closed || doc.revision != revision {
					operation.Finish("not applied: document changed", result.Model, result.Fallback)
					return
				}
				operation.Finish("ready to review", result.Model, result.Fallback)
				w.reviewAITransform(doc, startOffset, endOffset, selected, result.Text)
			})
		}()
	})
	dialog.Present(w.win)
	instruction.GrabFocus()
}

func (w *window) reviewAITransform(doc *editorDocument, start, end int, original, replacement string) {
	dialog := adw.NewAlertDialog("Review AI Replacement", "The document remains unchanged until you apply this replacement.")
	preview := gtk.NewTextView()
	preview.SetEditable(false)
	preview.SetMonospace(true)
	preview.SetWrapMode(gtk.WrapWordChar)
	preview.Buffer().SetText(replacement)
	scroll := gtk.NewScrolledWindow()
	scroll.SetMinContentHeight(280)
	scroll.SetMaxContentHeight(520)
	scroll.SetChild(preview)
	dialog.SetExtraChild(scroll)
	dialog.AddResponse("cancel", "Reject")
	dialog.AddResponse("apply", "Apply")
	dialog.SetCloseResponse("cancel")
	dialog.SetDefaultResponse("apply")
	dialog.SetResponseAppearance("apply", adw.ResponseSuggested)
	revision := doc.revision
	dialog.ConnectResponse(func(response string) {
		if response != "apply" || doc.closed || doc.revision != revision {
			return
		}
		current := doc.buffer.Text(doc.buffer.IterAtOffset(start), doc.buffer.IterAtOffset(end), true)
		if current != original {
			w.toast("Selection changed; replacement was not applied")
			return
		}
		doc.buffer.BeginUserAction()
		doc.buffer.Delete(doc.buffer.IterAtOffset(start), doc.buffer.IterAtOffset(end))
		doc.buffer.Insert(doc.buffer.IterAtOffset(start), replacement)
		doc.buffer.EndUserAction()
	})
	dialog.Present(w.win)
}
