package ui

import (
	"fmt"
	"strings"
	"unicode"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

const (
	ghostAlpha               = 0.5
	ghostHintAlpha           = 0.3
	maxInlineCompletionRunes = 2048
	maxInlinePrefixRunes     = 2048
)

// ghostCanvas draws one layout above the document text in buffer coordinates.
type ghostCanvas interface {
	SetGhost(layout *pango.Layout, x, y int, color *gdk.RGBA) bool
	ClearGhost()
}

// inlineCompletionRenderer shows one line of a suggestion beside the caret at
// a real line end. The source view draws it in its own text pass, so the
// document layout, fonts, and colors never change. Further lines are summarized
// by a "+N lines" hint and are still inserted on accept.
type inlineCompletionRenderer struct {
	view     *gtk.TextView
	canvas   ghostCanvas
	tabWidth func() int
	dismiss  func()

	tick                  uint
	frameClock            *gdk.FrameClock
	layoutSignal          coreglib.SignalHandle
	viewWidth, viewHeight int
	wrapMode              gtk.WrapMode

	// text is the visible ghost text; it is empty while nothing is shown.
	text string
}

func newInlineCompletionRenderer(view *gtk.TextView, canvas ghostCanvas, tabWidth func() int, dismiss func()) *inlineCompletionRenderer {
	return &inlineCompletionRenderer{view: view, canvas: canvas, tabWidth: tabWidth, dismiss: dismiss}
}

func (r *inlineCompletionRenderer) visible() bool { return r.text != "" }

func (r *inlineCompletionRenderer) show(suggestion string, cursor *gtk.TextIter) bool {
	r.hide()
	if suggestion == "" || !cursor.EndsLine() {
		return false
	}
	shown, hint := ghostDisplay(suggestion)
	if shown == "" && hint == "" {
		return false
	}
	if strings.ContainsRune(shown, '\t') {
		shown = expandTabs(shown, r.caretColumn(cursor), r.tabWidth())
	}

	displayStart := cursor.Copy()
	r.view.BackwardDisplayLineStart(displayStart)
	caret, _ := r.view.CursorLocations(cursor)
	// A right-to-left paragraph has no safe space to the right of the caret.
	if caret.X() < r.view.IterLocation(displayStart).X() {
		return false
	}

	text := shown + hint
	layout := r.view.CreatePangoLayout(text)
	if r.view.WrapMode() != gtk.WrapNone {
		visible := r.view.VisibleRect()
		width := visible.X() + visible.Width() - r.view.RightMargin() - caret.X()
		if width <= 0 {
			return false
		}
		layout.SetWidth(width * pango.SCALE)
		layout.SetEllipsize(pango.EllipsizeEnd)
	}
	foreground := r.view.Color()
	if hint != "" {
		attrs := pango.NewAttrList()
		setAttributeRange(attrs, pango.NewAttrForeground(
			color16(float64(foreground.Red())), color16(float64(foreground.Green())), color16(float64(foreground.Blue())),
		), len(shown), len(text))
		setAttributeRange(attrs, pango.NewAttrForegroundAlpha(color16(float64(foreground.Alpha())*ghostHintAlpha)), len(shown), len(text))
		layout.SetAttributes(attrs)
	}
	color := gdk.NewRGBA(foreground.Red(), foreground.Green(), foreground.Blue(), foreground.Alpha()*ghostAlpha)
	if !r.canvas.SetGhost(layout, caret.X(), caret.Y(), &color) {
		return false
	}
	r.text = text
	r.watchLayout()
	return true
}

// watchLayout dismisses the suggestion when a resize or wrap change moves the
// caret away from the drawn position.
func (r *inlineCompletionRenderer) watchLayout() {
	r.viewWidth, r.viewHeight = r.view.Width(), r.view.Height()
	r.wrapMode = r.view.WrapMode()
	r.tick = r.view.AddTickCallback(func(_ gtk.Widgetter, clock gdk.FrameClocker) bool {
		r.tick = 0
		// The first tick supplies a clock even if the view was not mapped when
		// the suggestion arrived.
		r.frameClock = gdk.BaseFrameClock(clock)
		r.layoutSignal = r.frameClock.ConnectAfter("layout", func() {
			if r.layoutChanged() {
				r.dismiss()
			}
		})
		if r.layoutChanged() {
			r.dismiss()
		}
		return false
	})
}

func (r *inlineCompletionRenderer) layoutChanged() bool {
	return r.view.Width() != r.viewWidth || r.view.Height() != r.viewHeight || r.view.WrapMode() != r.wrapMode
}

// caretColumn returns the caret's visual column for tab expansion.
func (r *inlineCompletionRenderer) caretColumn(cursor *gtk.TextIter) int {
	if cursor.LineOffset() > maxInlinePrefixRunes {
		return 0
	}
	lineStart := cursor.Copy()
	lineStart.SetLineOffset(0)
	prefix := r.view.Buffer().Text(lineStart, cursor, true)
	return visualWidth(prefix, r.tabWidth())
}

func (r *inlineCompletionRenderer) hide() {
	if r.tick != 0 {
		r.view.RemoveTickCallback(r.tick)
		r.tick = 0
	}
	if r.frameClock != nil {
		r.frameClock.HandlerDisconnect(r.layoutSignal)
		r.frameClock = nil
		r.layoutSignal = 0
	}
	if r.text != "" {
		r.canvas.ClearGhost()
		r.text = ""
	}
}

func (r *inlineCompletionRenderer) close() { r.hide() }

// ghostDisplay returns the line drawn beside the caret and a hint for the
// lines that are not drawn. A suggestion that starts with a line break shows
// its first new line after a return arrow.
func ghostDisplay(suggestion string) (shown, hint string) {
	lines := strings.Split(strings.ReplaceAll(suggestion, "\r\n", "\n"), "\n")
	for index := range lines {
		lines[index] = strings.TrimSuffix(lines[index], "\r")
	}
	shown, rest := lines[0], lines[1:]
	if shown == "" && len(rest) > 0 {
		shown, rest = "↵ "+rest[0], rest[1:]
	}
	switch len(rest) {
	case 0:
	case 1:
		hint = "  +1 line"
	default:
		hint = fmt.Sprintf("  +%d lines", len(rest))
	}
	return shown, hint
}

// expandTabs replaces tabs with spaces up to the next tab stop, counting from
// the caret's visual column.
func expandTabs(text string, column, tabWidth int) string {
	if tabWidth <= 0 {
		tabWidth = 8
	}
	var out strings.Builder
	for _, r := range text {
		if r == '\t' {
			spaces := tabWidth - column%tabWidth
			out.WriteString(strings.Repeat(" ", spaces))
			column += spaces
			continue
		}
		out.WriteRune(r)
		column++
	}
	return out.String()
}

func visualWidth(text string, tabWidth int) int {
	if tabWidth <= 0 {
		tabWidth = 8
	}
	column := 0
	for _, r := range text {
		if r == '\t' {
			column += tabWidth - column%tabWidth
		} else {
			column++
		}
	}
	return column
}

func inlineCompletionText(text string) string {
	runes := []rune(text)
	if len(runes) > maxInlineCompletionRunes {
		text = string(runes[:maxInlineCompletionRunes])
	}
	return text
}

func setAttributeRange(attributes *pango.AttrList, attribute *pango.Attribute, start, end int) {
	if end <= start {
		return
	}
	attribute.SetStartIndex(uint(start))
	attribute.SetEndIndex(uint(end))
	attributes.Insert(attribute)
}

func color16(value float64) uint16 { return uint16(max(0, min(1, value)) * 65535) }

type eSpan struct {
	byteStart int
	byteEnd   int
	runeStart int
	runeEnd   int
}

func standaloneESpans(text string) []eSpan {
	runes := []rune(text)
	spans := make([]eSpan, 0)
	byteOffset := 0
	for index, current := range runes {
		width := len(string(current))
		if current == 'E' && isStandaloneAt(runes, index) {
			spans = append(spans, eSpan{
				byteStart: byteOffset, byteEnd: byteOffset + width,
				runeStart: index, runeEnd: index + 1,
			})
		}
		byteOffset += width
	}
	return spans
}

func isStandaloneAt(text []rune, index int) bool {
	word := func(value rune) bool { return value == '_' || unicode.IsLetter(value) || unicode.IsNumber(value) }
	return (index == 0 || !word(text[index-1])) && (index+1 == len(text) || !word(text[index+1]))
}
