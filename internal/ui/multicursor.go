package ui

import (
	"context"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
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

func isIdentifierRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// selectedWord returns the selection, or the identifier around the caret.
// wholeWord reports that the text came from an empty caret, so matches must
// stand alone as identifiers.
func (d *editorDocument) selectedWord() (word string, cursor multiCursor, wholeWord, ok bool) {
	cursor = d.currentCursor()
	start, end := cursor.bounds()
	if start == end {
		a := d.buffer.IterAtOffset(start)
		for {
			previous := a.Copy()
			if !previous.BackwardChar() || !isIdentifierRune(rune(previous.Char())) {
				break
			}
			a = previous
		}
		b := d.buffer.IterAtOffset(end)
		for isIdentifierRune(rune(b.Char())) && b.ForwardChar() {
		}
		start, end = a.Offset(), b.Offset()
		cursor = multiCursor{anchor: start, head: end}
		wholeWord = true
	}
	if start == end {
		return "", multiCursor{}, false, false
	}
	return d.buffer.Text(d.buffer.IterAtOffset(start), d.buffer.IterAtOffset(end), true), cursor, wholeWord, true
}

// isStandaloneMatch reports whether start..end has no identifier rune on
// either side.
func (d *editorDocument) isStandaloneMatch(start, end *gtk.TextIter) bool {
	before := start.Copy()
	if before.BackwardChar() && isIdentifierRune(rune(before.Char())) {
		return false
	}
	return !isIdentifierRune(rune(end.Char()))
}

func (d *editorDocument) selectNextOccurrence() {
	word, current, wholeWord, ok := d.selectedWord()
	if !ok {
		return
	}
	if len(d.multiCursors) <= 1 {
		// A single entry is left over from an earlier Ctrl+D that found no
		// second match; it is not visible, so start again from the caret.
		d.multiCursors = []multiCursor{current}
	}
	if len(d.multiCursors) >= maxMultipleCursors {
		return
	}
	// Search the buffer from the last cursor and wrap once, instead of
	// copying the whole document on each press.
	_, lastEnd := d.multiCursors[len(d.multiCursors)-1].bounds()
	from := d.buffer.IterAtOffset(lastEnd)
	for wrapped := false; ; {
		matchStart, matchEnd, found := from.ForwardSearch(word, gtk.TextSearchTextOnly, nil)
		if !found {
			if wrapped {
				return
			}
			wrapped = true
			from = d.buffer.StartIter()
			continue
		}
		if wrapped && matchStart.Offset() >= lastEnd {
			return
		}
		if (!wholeWord || d.isStandaloneMatch(matchStart, matchEnd)) &&
			!d.containsCursor(matchStart.Offset(), matchEnd.Offset()) {
			d.multiCursors = append(d.multiCursors, multiCursor{anchor: matchStart.Offset(), head: matchEnd.Offset()})
			d.renderMultipleSelections()
			d.view.Widget.ScrollMarkOnscreen(d.buffer.GetInsert())
			return
		}
		from = matchEnd
	}
}

func (d *editorDocument) selectAllOccurrences() {
	word, _, wholeWord, ok := d.selectedWord()
	if !ok {
		return
	}
	start, end := d.buffer.Bounds()
	matches := findExactMatches(d.buffer.Text(start, end, true), word, wholeWord)
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
		state := click.CurrentEventState()
		if state&gdk.AltMask == 0 {
			d.clearMultipleSelections()
			return
		}
		if state&gdk.ShiftMask != 0 {
			// Shift+Alt starts a column selection; the drag gesture owns it.
			return
		}
		bx, by := d.view.Widget.WindowToBufferCoords(gtk.TextWindowWidget, int(x), int(y))
		iter, _ := d.view.Widget.IterAtLocation(bx, by)
		if iter == nil {
			return
		}
		if len(d.multiCursors) <= 1 {
			d.multiCursors = []multiCursor{d.currentCursor()}
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

	// Shift+Alt+drag selects a column: one cursor per line between the start
	// and current pointer positions.
	drag := gtk.NewGestureDrag()
	drag.SetPropagationPhase(gtk.PhaseCapture)
	var boxActive bool
	var boxX, boxY int
	drag.ConnectDragBegin(func(x, y float64) {
		state := drag.CurrentEventState()
		boxActive = state&(gdk.ShiftMask|gdk.AltMask) == gdk.ShiftMask|gdk.AltMask
		if !boxActive {
			drag.SetState(gtk.EventSequenceDenied)
			return
		}
		drag.SetState(gtk.EventSequenceClaimed)
		boxX, boxY = d.view.Widget.WindowToBufferCoords(gtk.TextWindowWidget, int(x), int(y))
		d.selectColumn(boxX, boxY, boxX, boxY)
	})
	drag.ConnectDragUpdate(func(offsetX, offsetY float64) {
		if !boxActive {
			return
		}
		startX, startY, _ := drag.StartPoint()
		x, y := d.view.Widget.WindowToBufferCoords(gtk.TextWindowWidget, int(startX+offsetX), int(startY+offsetY))
		d.selectColumn(boxX, boxY, x, y)
	})
	d.view.Widget.AddController(drag)
}

// selectColumn places one cursor per line between y0 and y1, selecting from
// x0 to x1 in buffer coordinates. Lines shorter than x0 get a caret at
// their end.
func (d *editorDocument) selectColumn(x0, y0, x1, y1 int) {
	view := d.view.Widget
	top, bottom := min(y0, y1), max(y0, y1)
	iter, _ := view.IterAtLocation(x0, top)
	cursors := make([]multiCursor, 0, 16)
	for len(cursors) < maxMultipleCursors {
		lineY, height := view.LineYrange(iter)
		y := max(lineY, top)
		anchor, _ := view.IterAtLocation(x0, y)
		head, _ := view.IterAtLocation(x1, y)
		cursors = append(cursors, multiCursor{anchor: anchor.Offset(), head: head.Offset()})
		if lineY+height > bottom || !iter.ForwardLine() {
			break
		}
	}
	if y0 > y1 {
		// Dragging upward makes the top line the primary cursor.
		for i, j := 0, len(cursors)-1; i < j; i, j = i+1, j-1 {
			cursors[i], cursors[j] = cursors[j], cursors[i]
		}
	}
	d.multiCursors = cursors
	if len(cursors) == 1 {
		d.clearMultipleSelections()
		d.buffer.SelectRange(d.buffer.IterAtOffset(cursors[0].head), d.buffer.IterAtOffset(cursors[0].anchor))
		return
	}
	d.renderMultipleSelections()
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

// addCursorVertically adds a cursor on the display line above the topmost
// cursor or below the bottommost one. The column stays where the first
// cursor was, even across shorter lines, as in VS Code.
func (d *editorDocument) addCursorVertically(direction int) {
	if len(d.multiCursors) <= 1 {
		d.multiCursors = []multiCursor{d.currentCursor()}
		d.verticalX = -1
	}
	if len(d.multiCursors) >= maxMultipleCursors {
		return
	}
	base := d.multiCursors[0].head
	for _, cursor := range d.multiCursors {
		if (direction < 0 && cursor.head < base) || (direction > 0 && cursor.head > base) {
			base = cursor.head
		}
	}
	view := d.view.Widget
	baseIter := d.buffer.IterAtOffset(base)
	rect := view.IterLocation(baseIter)
	if d.verticalX < 0 {
		d.verticalX = rect.X()
	}
	// Move by display line through GTK, which lays the line out as needed,
	// then find the kept column on it.
	line := baseIter.Copy()
	var moved bool
	if direction > 0 {
		// GTK reports false on reaching the end, even for a last empty line.
		moved = view.ForwardDisplayLine(line) || (line.IsEnd() && line.Line() != baseIter.Line())
	} else {
		moved = view.BackwardDisplayLine(line)
	}
	if !moved {
		return
	}
	target := d.columnOnDisplayLine(line, d.verticalX)
	offset := target.Offset()
	if d.containsCursor(offset, offset) {
		return
	}
	d.multiCursors = append(d.multiCursors, multiCursor{anchor: offset, head: offset})
	d.renderMultipleSelections()
	d.view.Widget.ScrollMarkOnscreen(d.buffer.GetInsert())
}

// columnOnDisplayLine walks from the start of a display line to the
// character boundary nearest to x. Pixel lookups by y are unreliable on
// lines GTK has not laid out yet, but per-character locations are exact.
func (d *editorDocument) columnOnDisplayLine(lineStart *gtk.TextIter, x int) *gtk.TextIter {
	view := d.view.Widget
	target := lineStart.Copy()
	for range maxInlinePrefixRunes {
		if target.EndsLine() {
			break
		}
		if r := view.IterLocation(target); r.X()+r.Width()/2 > x {
			break
		}
		next := target.Copy()
		next.ForwardChar()
		if view.StartsDisplayLine(next) {
			break
		}
		target = next
	}
	return target
}

// removeLastCursor drops the most recently added cursor (Ctrl+U).
func (d *editorDocument) removeLastCursor() {
	if len(d.multiCursors) < 2 {
		return
	}
	d.multiCursors = d.multiCursors[:len(d.multiCursors)-1]
	if len(d.multiCursors) == 1 {
		last := d.multiCursors[0]
		d.clearMultipleSelections()
		d.buffer.SelectRange(d.buffer.IterAtOffset(last.head), d.buffer.IterAtOffset(last.anchor))
		return
	}
	d.renderMultipleSelections()
	d.view.Widget.ScrollMarkOnscreen(d.buffer.GetInsert())
}

// renderMultipleSelections shows secondary selections with a tag and their
// carets in the source view's drawing layer. The last cursor is the primary
// one: it is GTK's own caret and selection.
func (d *editorDocument) renderMultipleSelections() {
	start, end := d.buffer.Bounds()
	d.buffer.RemoveTag(d.multiTag, start, end)
	heads := make([]int, 0, len(d.multiCursors))
	for i, cursor := range d.multiCursors {
		a, b := cursor.bounds()
		if b > a {
			d.buffer.ApplyTag(d.multiTag, d.buffer.IterAtOffset(a), d.buffer.IterAtOffset(b))
		}
		if i < len(d.multiCursors)-1 {
			heads = append(heads, cursor.head)
		}
	}
	d.view.SetCarets(heads)
	if len(d.multiCursors) > 0 {
		last := d.multiCursors[len(d.multiCursors)-1]
		d.buffer.SelectRange(d.buffer.IterAtOffset(last.head), d.buffer.IterAtOffset(last.anchor))
	}
	d.window.updateTitle(d)
}

func (d *editorDocument) clearMultipleSelections() {
	d.verticalX = -1
	if len(d.multiCursors) == 0 {
		return
	}
	start, end := d.buffer.Bounds()
	d.buffer.RemoveTag(d.multiTag, start, end)
	d.view.SetCarets(nil)
	d.multiCursors = nil
	d.window.updateTitle(d)
}

func (d *editorDocument) editMultiple(keyval uint, state gdk.ModifierType) bool {
	ctrl, shift := state&gdk.ControlMask != 0, state&gdk.ShiftMask != 0
	if state&(gdk.AltMask|gdk.SuperMask) != 0 {
		return false
	}
	if ctrl {
		switch gdk.KeyvalToLower(keyval) {
		case gdk.KEY_a:
			// Select All replaces every cursor with one selection.
			d.clearMultipleSelections()
			return false
		case gdk.KEY_u:
			d.removeLastCursor()
			return true
		case gdk.KEY_BackSpace:
			d.deleteWordMultiple(gdk.KEY_Left)
			return true
		case gdk.KEY_Delete:
			d.deleteWordMultiple(gdk.KEY_Right)
			return true
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
		d.newlineMultiple()
	case gdk.KEY_Page_Up, gdk.KEY_Page_Down:
		// Paging moves only the primary caret, so leave multi-cursor mode.
		d.clearMultipleSelections()
		return false
	case gdk.KEY_BackSpace:
		d.deleteAtMultiple(true)
	case gdk.KEY_Delete:
		d.deleteAtMultiple(false)
	case gdk.KEY_Tab, gdk.KEY_ISO_Left_Tab:
		switch {
		case shift || keyval == gdk.KEY_ISO_Left_Tab:
			d.indentMultiple(true)
		case d.anySelection():
			d.indentMultiple(false)
		default:
			d.replaceMultiple(d.indentUnit())
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
	d.normalizeCursors()
	d.renderMultipleSelections()
}

// normalizeCursors merges cursors that are identical or overlap. Edits on
// overlapping ranges would otherwise delete the wrong text. The order of the
// remaining cursors is kept, and the primary (last) cursor stays last.
func (d *editorDocument) normalizeCursors() {
	if len(d.multiCursors) < 2 {
		return
	}
	type group struct {
		cursor        multiCursor
		start, end    int
		order         int
		merged, first bool
	}
	primary := len(d.multiCursors) - 1
	order := make([]int, len(d.multiCursors))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, _ := d.multiCursors[order[i]].bounds()
		b, _ := d.multiCursors[order[j]].bounds()
		return a < b
	})
	var groups []group
	for _, index := range order {
		cursor := d.multiCursors[index]
		start, end := cursor.bounds()
		if n := len(groups); n > 0 {
			last := &groups[n-1]
			if start < last.end || (start == last.start && end == last.end) {
				last.end = max(last.end, end)
				last.merged = true
				last.first = last.first || index == primary
				last.order = min(last.order, index)
				continue
			}
		}
		groups = append(groups, group{cursor: cursor, start: start, end: end, order: index, first: index == primary})
	}
	if len(groups) == len(d.multiCursors) {
		return
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].first != groups[j].first {
			return groups[j].first
		}
		return groups[i].order < groups[j].order
	})
	d.multiCursors = d.multiCursors[:0]
	for _, g := range groups {
		if g.merged {
			g.cursor = multiCursor{anchor: g.start, head: g.end}
		}
		d.multiCursors = append(d.multiCursors, g.cursor)
	}
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
			break
		}
		// Smart Home: the first non-blank character, then the line start.
		lineStart := iter.Copy()
		lineStart.SetLineOffset(0)
		first := lineStart.Copy()
		for !first.EndsLine() && (first.Char() == ' ' || first.Char() == '\t') {
			first.ForwardChar()
		}
		if iter.Offset() == first.Offset() {
			return lineStart.Offset()
		}
		return first.Offset()
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
	d.normalizeCursors()
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
	d.normalizeCursors()
	d.renderMultipleSelections()
	d.afterMultipleEdit()
}

// newlineMultiple breaks the line at each cursor and keeps that line's
// indentation, as a single cursor does.
func (d *editorDocument) newlineMultiple() {
	d.replaceMultipleTexts(func(_ int, cursor multiCursor) string {
		start, _ := cursor.bounds()
		lineStart := d.buffer.IterAtOffset(start)
		lineStart.SetLineOffset(0)
		prefix := d.buffer.Text(lineStart, d.buffer.IterAtOffset(start), true)
		return "\n" + prefix[:len(prefix)-len(strings.TrimLeft(prefix, " \t"))]
	})
}

// deleteWordMultiple deletes to the previous word start (KEY_Left) or next
// word end (KEY_Right) at each empty cursor, and deletes each selection.
func (d *editorDocument) deleteWordMultiple(direction uint) {
	for i, cursor := range d.multiCursors {
		if a, b := cursor.bounds(); a == b {
			d.multiCursors[i] = multiCursor{anchor: a, head: d.movedOffset(cursor, direction, true, true)}
		}
	}
	d.replaceMultiple("")
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

func (d *editorDocument) anySelection() bool {
	for _, cursor := range d.multiCursors {
		if cursor.anchor != cursor.head {
			return true
		}
	}
	return false
}

func (d *editorDocument) indentUnit() string {
	if spaces, width := d.indentation(); spaces {
		return strings.Repeat(" ", width)
	}
	return "\t"
}

// indentMultiple indents or outdents every line that holds a cursor or is
// touched by a selection, once per line. Marks carry the cursors across the
// edits.
func (d *editorDocument) indentMultiple(outdent bool) {
	_, width := d.indentation()
	unit := d.indentUnit()
	lineSet := make(map[int]bool)
	for _, cursor := range d.multiCursors {
		a, b := cursor.bounds()
		first := d.buffer.IterAtOffset(a).Line()
		endIter := d.buffer.IterAtOffset(b)
		last := endIter.Line()
		if b > a && endIter.LineOffset() == 0 && last > first {
			last--
		}
		for line := first; line <= last; line++ {
			lineSet[line] = true
		}
	}
	lines := make([]int, 0, len(lineSet))
	for line := range lineSet {
		lines = append(lines, line)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(lines)))
	marks := make([][2]*gtk.TextMark, len(d.multiCursors))
	for i, cursor := range d.multiCursors {
		marks[i] = [2]*gtk.TextMark{
			d.buffer.CreateMark("", d.buffer.IterAtOffset(cursor.anchor), false),
			d.buffer.CreateMark("", d.buffer.IterAtOffset(cursor.head), false),
		}
	}
	d.bulkEdit = true
	d.buffer.BeginUserAction()
	for _, line := range lines {
		start, ok := d.buffer.IterAtLine(line)
		if !ok {
			continue
		}
		if !outdent {
			if !start.EndsLine() {
				d.buffer.Insert(start, unit)
			}
			continue
		}
		end := start.Copy()
		if end.Char() == '\t' {
			end.ForwardChar()
		} else {
			for removed := 0; removed < width && end.Char() == ' '; removed++ {
				end.ForwardChar()
			}
		}
		if end.Offset() > start.Offset() {
			d.buffer.Delete(start, end)
		}
	}
	d.buffer.EndUserAction()
	d.bulkEdit = false
	for i, pair := range marks {
		d.multiCursors[i] = multiCursor{
			anchor: d.buffer.IterAtMark(pair[0]).Offset(),
			head:   d.buffer.IterAtMark(pair[1]).Offset(),
		}
		d.buffer.DeleteMark(pair[0])
		d.buffer.DeleteMark(pair[1])
	}
	d.normalizeCursors()
	d.renderMultipleSelections()
	d.afterMultipleEdit()
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

// afterMultipleEdit refreshes what the changed handler skips during a bulk
// edit. LSP already received each edit incrementally.
func (d *editorDocument) afterMultipleEdit() {
	if d.search != nil && d.search.query != "" {
		d.search.scheduleRefresh()
	}
	d.scheduleMarkdownPreview()
}
