package ui

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/lsp"
)

const maxMultipleCursors = 1000

// multiCursor keeps direction as well as bounds, matching GTK's anchor/head
// selection model. This makes Shift+movement behave like VS Code.
type multiCursor struct{ anchor, head int }

func (c multiCursor) bounds() (int, int) {
	if c.anchor <= c.head {
		return c.anchor, c.head
	}
	return c.head, c.anchor
}

func (d *editorDocument) currentCursor() multiCursor {
	return multiCursor{
		anchor: d.buffer.IterAtMark(d.buffer.SelectionBound()).Offset(),
		head:   d.buffer.IterAtMark(d.buffer.GetInsert()).Offset(),
	}
}

func (d *editorDocument) selectedWord() (string, multiCursor, bool) {
	cursor := d.currentCursor()
	start, end := cursor.bounds()
	if start == end {
		a, b := d.buffer.IterAtOffset(start), d.buffer.IterAtOffset(end)
		a.BackwardWordStart()
		b.ForwardWordEnd()
		start, end = a.Offset(), b.Offset()
		cursor = multiCursor{anchor: start, head: end}
	}
	if start == end {
		return "", multiCursor{}, false
	}
	return d.buffer.Text(d.buffer.IterAtOffset(start), d.buffer.IterAtOffset(end), true), cursor, true
}

func (d *editorDocument) selectNextOccurrence() {
	word, current, ok := d.selectedWord()
	if !ok || word == "" {
		return
	}
	if len(d.multiCursors) == 0 {
		d.multiCursors = append(d.multiCursors, current)
	}
	start, end := d.buffer.Bounds()
	matches := findMatches(d.buffer.Text(start, end, true), word)
	_, lastEnd := d.multiCursors[len(d.multiCursors)-1].bounds()
	chosen := -1
	for i, match := range matches {
		if match.start >= lastEnd && !d.containsCursor(match.start, match.end) {
			chosen = i
			break
		}
	}
	if chosen < 0 {
		for i, match := range matches {
			if !d.containsCursor(match.start, match.end) {
				chosen = i
				break
			}
		}
	}
	if chosen >= 0 && len(d.multiCursors) < maxMultipleCursors {
		m := matches[chosen]
		d.multiCursors = append(d.multiCursors, multiCursor{anchor: m.start, head: m.end})
		d.renderMultipleSelections()
	}
}

func (d *editorDocument) selectAllOccurrences() {
	word, _, ok := d.selectedWord()
	if !ok || word == "" {
		return
	}
	start, end := d.buffer.Bounds()
	matches := findMatches(d.buffer.Text(start, end, true), word)
	if len(matches) > maxMultipleCursors {
		matches = matches[:maxMultipleCursors]
		d.window.toast("Limited multiple cursors to 1,000 matches")
	}
	d.multiCursors = make([]multiCursor, len(matches))
	for i, match := range matches {
		d.multiCursors[i] = multiCursor{anchor: match.start, head: match.end}
	}
	d.renderMultipleSelections()
}

func (d *editorDocument) containsCursor(start, end int) bool {
	for _, cursor := range d.multiCursors {
		a, b := cursor.bounds()
		if a == start && b == end {
			return true
		}
	}
	return false
}

// Alt+click adds or removes a cursor at the pointer. An ordinary click exits
// multi-cursor mode and is left to GtkTextView for normal selection handling.
func (d *editorDocument) connectPointerSelection() {
	click := gtk.NewGestureClick()
	click.SetPropagationPhase(gtk.PhaseCapture)
	click.ConnectPressed(func(_ int, x, y float64) {
		if click.CurrentEventState()&gdk.AltMask == 0 {
			d.clearMultipleSelections()
			return
		}
		bx, by := d.view.Widget.WindowToBufferCoords(gtk.TextWindowWidget, int(x), int(y))
		iter, _ := d.view.Widget.IterAtLocation(bx, by)
		if iter == nil {
			return
		}
		if len(d.multiCursors) == 0 {
			d.multiCursors = append(d.multiCursors, d.currentCursor())
		}
		offset := iter.Offset()
		removed := false
		for i, cursor := range d.multiCursors {
			if cursor.anchor == offset && cursor.head == offset {
				d.multiCursors = append(d.multiCursors[:i], d.multiCursors[i+1:]...)
				removed = true
				break
			}
		}
		if !removed && !d.cursorIntersects(offset) && len(d.multiCursors) < maxMultipleCursors {
			d.multiCursors = append(d.multiCursors, multiCursor{anchor: offset, head: offset})
		}
		d.renderMultipleSelections()
		click.SetState(gtk.EventSequenceClaimed)
	})
	d.view.Widget.AddController(click)
}

func (d *editorDocument) cursorIntersects(offset int) bool {
	for _, cursor := range d.multiCursors {
		start, end := cursor.bounds()
		if offset == start || (offset > start && offset < end) {
			return true
		}
	}
	return false
}

func (d *editorDocument) addCursorVertically(direction int) {
	if len(d.multiCursors) == 0 {
		d.multiCursors = append(d.multiCursors, d.currentCursor())
	}
	base := d.multiCursors[len(d.multiCursors)-1]
	iter := d.buffer.IterAtOffset(base.head)
	line, column := iter.Line()+direction, iter.LineOffset()
	if line < 0 || line >= d.buffer.LineCount() {
		return
	}
	target, _ := d.buffer.IterAtLineOffset(line, column)
	if target == nil {
		target, _ = d.buffer.IterAtLine(line)
		target.ForwardToLineEnd()
	}
	if !d.containsCursor(target.Offset(), target.Offset()) && len(d.multiCursors) < maxMultipleCursors {
		d.multiCursors = append(d.multiCursors, multiCursor{anchor: target.Offset(), head: target.Offset()})
		d.renderMultipleSelections()
	}
}

func (d *editorDocument) renderMultipleSelections() {
	start, end := d.buffer.Bounds()
	d.buffer.RemoveTag(d.multiTag, start, end)
	for _, caret := range d.carets {
		caret.SetVisible(false)
	}
	for i, cursor := range d.multiCursors {
		a, b := cursor.bounds()
		if b > a {
			d.buffer.ApplyTag(d.multiTag, d.buffer.IterAtOffset(a), d.buffer.IterAtOffset(b))
		}
		if i == len(d.multiCursors)-1 {
			continue
		}
		for len(d.carets) <= i {
			caret := gtk.NewLabel("│")
			caret.AddCSSClass("secondary-caret")
			d.view.Widget.AddOverlay(caret, 0, 0)
			d.carets = append(d.carets, caret)
		}
		rect := d.view.Widget.IterLocation(d.buffer.IterAtOffset(cursor.head))
		d.view.Widget.MoveOverlay(d.carets[i], rect.X()-1, rect.Y())
		d.carets[i].SetVisible(true)
	}
	if len(d.multiCursors) > 0 {
		last := d.multiCursors[len(d.multiCursors)-1]
		d.buffer.SelectRange(d.buffer.IterAtOffset(last.head), d.buffer.IterAtOffset(last.anchor))
	}
}

func (d *editorDocument) clearMultipleSelections() {
	if len(d.multiCursors) == 0 {
		return
	}
	start, end := d.buffer.Bounds()
	d.buffer.RemoveTag(d.multiTag, start, end)
	for _, caret := range d.carets {
		caret.SetVisible(false)
	}
	d.multiCursors = nil
}

func (d *editorDocument) editMultiple(keyval uint, state gdk.ModifierType) bool {
	ctrl, shift := state&gdk.ControlMask != 0, state&gdk.ShiftMask != 0
	if state&(gdk.AltMask|gdk.SuperMask) != 0 {
		return false
	}
	if ctrl {
		switch gdk.KeyvalToLower(keyval) {
		case gdk.KEY_c:
			d.copyMultiple()
			return true
		case gdk.KEY_x:
			d.copyMultiple()
			d.replaceMultiple("")
			return true
		case gdk.KEY_v:
			d.pasteMultiple()
			return true
		case gdk.KEY_Left, gdk.KEY_Right, gdk.KEY_Home, gdk.KEY_End:
			d.moveMultiple(keyval, shift, true)
			return true
		default:
			return false
		}
	}
	switch keyval {
	case gdk.KEY_Left, gdk.KEY_Right, gdk.KEY_Up, gdk.KEY_Down, gdk.KEY_Home, gdk.KEY_End:
		d.moveMultiple(keyval, shift, false)
	case gdk.KEY_Return, gdk.KEY_KP_Enter:
		d.replaceMultiple("\n")
	case gdk.KEY_BackSpace:
		d.deleteAtMultiple(true)
	case gdk.KEY_Delete:
		d.deleteAtMultiple(false)
	case gdk.KEY_Tab:
		if shift {
			d.outdentMultiple()
		} else if d.window.options.Config.Editor.InsertSpaces {
			d.replaceMultiple(strings.Repeat(" ", d.window.options.Config.Editor.TabWidth))
		} else {
			d.replaceMultiple("\t")
		}
	case gdk.KEY_Escape:
		d.clearMultipleSelections()
	default:
		r := rune(gdk.KeyvalToUnicode(keyval))
		if r == 0 || !utf8.ValidRune(r) {
			return false
		}
		d.replaceMultiple(string(r))
	}
	return true
}

func (d *editorDocument) moveMultiple(keyval uint, extend, byWord bool) {
	for i, cursor := range d.multiCursors {
		position := d.movedOffset(cursor, keyval, extend, byWord)
		if extend {
			d.multiCursors[i].head = position
		} else {
			d.multiCursors[i] = multiCursor{anchor: position, head: position}
		}
	}
	d.renderMultipleSelections()
}

func (d *editorDocument) movedOffset(cursor multiCursor, keyval uint, extend, byWord bool) int {
	start, end := cursor.bounds()
	if !extend && start != end {
		if keyval == gdk.KEY_Left || keyval == gdk.KEY_Home {
			return start
		}
		if keyval == gdk.KEY_Right || keyval == gdk.KEY_End {
			return end
		}
	}
	iter := d.buffer.IterAtOffset(cursor.head)
	switch keyval {
	case gdk.KEY_Left:
		if byWord {
			iter.BackwardWordStart()
		} else {
			iter.BackwardChar()
		}
	case gdk.KEY_Right:
		if byWord {
			iter.ForwardWordEnd()
		} else {
			iter.ForwardChar()
		}
	case gdk.KEY_Home:
		if byWord {
			iter.SetOffset(0)
		} else {
			iter.SetLineOffset(0)
		}
	case gdk.KEY_End:
		if byWord {
			iter.SetOffset(d.buffer.CharCount())
		} else {
			iter.ForwardToLineEnd()
		}
	case gdk.KEY_Up, gdk.KEY_Down:
		line := iter.Line()
		if keyval == gdk.KEY_Up {
			line--
		} else {
			line++
		}
		if line < 0 {
			return 0
		}
		if line >= d.buffer.LineCount() {
			return d.buffer.CharCount()
		}
		column := iter.LineOffset()
		if target, ok := d.buffer.IterAtLineOffset(line, column); ok {
			return target.Offset()
		}
		target, _ := d.buffer.IterAtLine(line)
		target.ForwardToLineEnd()
		return target.Offset()
	}
	return iter.Offset()
}

func (d *editorDocument) replaceMultiple(replacement string) {
	d.replaceMultipleTexts(func(int, multiCursor) string { return replacement })
}

func (d *editorDocument) replaceMultipleTexts(replacement func(int, multiCursor) string) {
	type edit struct {
		start, end, index int
		text              string
	}
	edits := make([]edit, len(d.multiCursors))
	for i, cursor := range d.multiCursors {
		a, b := cursor.bounds()
		edits[i] = edit{start: a, end: b, index: i, text: replacement(i, cursor)}
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	d.bulkEdit = true
	d.buffer.BeginUserAction()
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		if e.end > e.start {
			d.buffer.Delete(d.buffer.IterAtOffset(e.start), d.buffer.IterAtOffset(e.end))
		}
		if e.text != "" {
			d.buffer.Insert(d.buffer.IterAtOffset(e.start), e.text)
		}
	}
	d.buffer.EndUserAction()
	d.bulkEdit = false
	delta := 0
	for _, e := range edits {
		position := e.start + delta + utf8.RuneCountInString(e.text)
		d.multiCursors[e.index] = multiCursor{anchor: position, head: position}
		delta += utf8.RuneCountInString(e.text) - (e.end - e.start)
	}
	d.renderMultipleSelections()
	d.afterMultipleEdit()
}

func (d *editorDocument) deleteAtMultiple(backward bool) {
	for i, cursor := range d.multiCursors {
		a, b := cursor.bounds()
		if a == b {
			if backward && a > 0 {
				a--
			}
			if !backward && b < d.buffer.CharCount() {
				b++
			}
			d.multiCursors[i] = multiCursor{anchor: a, head: b}
		}
	}
	d.replaceMultiple("")
}

func (d *editorDocument) outdentMultiple() {
	width := d.window.options.Config.Editor.TabWidth
	changed := false
	for i, cursor := range d.multiCursors {
		a, b := cursor.bounds()
		if a != b {
			continue
		}
		line := d.buffer.IterAtOffset(a)
		line.SetLineOffset(0)
		prefix := d.buffer.Text(line, d.buffer.IterAtOffset(a), true)
		remove := 0
		if strings.HasSuffix(prefix, "\t") {
			remove = 1
		} else {
			for remove < width && remove < len(prefix) && prefix[len(prefix)-1-remove] == ' ' {
				remove++
			}
		}
		if remove > 0 {
			d.multiCursors[i] = multiCursor{anchor: a - remove, head: a}
			changed = true
		}
	}
	if changed {
		d.replaceMultiple("")
	}
}

func (d *editorDocument) copyMultiple() {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	parts := make([]string, 0, len(d.multiCursors))
	for _, cursor := range d.multiCursors {
		a, b := cursor.bounds()
		if a != b {
			parts = append(parts, d.buffer.Text(d.buffer.IterAtOffset(a), d.buffer.IterAtOffset(b), true))
		}
	}
	if len(parts) > 0 {
		display.Clipboard().SetText(strings.Join(parts, "\n"))
	}
}

func (d *editorDocument) pasteMultiple() {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	display.Clipboard().ReadTextAsync(context.Background(), func(result gio.AsyncResulter) {
		text, err := display.Clipboard().ReadTextFinish(result)
		if err != nil || d.closed {
			return
		}
		lines := strings.Split(text, "\n")
		if len(lines) == len(d.multiCursors) {
			d.replaceMultipleTexts(func(i int, _ multiCursor) string { return lines[i] })
		} else {
			d.replaceMultiple(text)
		}
	})
}

func (d *editorDocument) afterMultipleEdit() {
	if d.lspReady {
		start, end := d.buffer.Bounds()
		d.lspVer++
		if err := d.lspClient.Change(d.lspURI, d.lspVer, []lsp.Change{{Text: d.buffer.Text(start, end, true)}}); err != nil {
			slog.Debug("LSP multiple-cursor change", "err", err)
		}
	}
	if d.search != nil && d.search.query != "" {
		d.search.scheduleRefresh()
	}
	d.scheduleMarkdownPreview()
}
