package ui

import (
	"strings"
	"unicode"

	"github.com/diamondburned/gotk4/pkg/cairo"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotk4/pkg/pangocairo"
)

const (
	ghostAlpha               = 0.68
	maxInlineCompletionRunes = 2048
	maxInlinePrefixRunes     = 2048
)

// inlineCompletionRenderer has two safe presentations. A single-line
// completion at the real line end is drawn beside the caret. Everything else
// is drawn in reserved space below the paragraph. Neither path masks, redraws,
// or restyles document text.
type inlineCompletionRenderer struct {
	view    *gtk.TextView
	buffer  *gtk.TextBuffer
	area    *gtk.DrawingArea
	space   *gtk.TextTag
	redraw  func()
	dismiss func()

	tick                  uint
	frameClock            *gdk.FrameClock
	layoutSignal          coreglib.SignalHandle
	viewWidth, viewHeight int
	wrapMode              gtk.WrapMode

	composed string
	width    int
	preview  bool

	spaceStart     *gtk.TextMark
	spaceEnd       *gtk.TextMark
	baseBottom     int
	extendedBottom bool
}

func newInlineCompletionRenderer(view *gtk.TextView, buffer *gtk.TextBuffer, dismiss func()) *inlineCompletionRenderer {
	renderer := &inlineCompletionRenderer{
		view: view, buffer: buffer, baseBottom: view.BottomMargin(), dismiss: dismiss,
	}
	renderer.space = gtk.NewTextTag("e-inline-completion-space")
	buffer.TagTable().Add(renderer.space)
	renderer.area = gtk.NewDrawingArea()
	renderer.area.AddCSSClass("editor")
	renderer.area.AddCSSClass("inline-completion")
	renderer.area.SetHAlign(gtk.AlignStart)
	renderer.area.SetVAlign(gtk.AlignStart)
	renderer.area.SetCanTarget(false)
	renderer.area.SetVisible(false)
	renderer.area.SetDrawFunc(renderer.draw)
	view.AddOverlay(renderer.area, 0, 0)
	renderer.redraw = view.QueueDraw
	return renderer
}

func (r *inlineCompletionRenderer) show(suggestion string, cursor *gtk.TextIter) bool {
	r.hide()
	if suggestion == "" {
		return false
	}

	lineEnd := cursor.Copy()
	if !lineEnd.EndsLine() {
		lineEnd.ForwardToLineEnd()
	}
	if cursor.Offset() == lineEnd.Offset() && !strings.ContainsAny(suggestion, "\r\n") {
		if r.showInline(suggestion, cursor) {
			return true
		}
	}
	r.showPreview(suggestion, cursor, lineEnd)
	return true
}

func (r *inlineCompletionRenderer) showInline(suggestion string, cursor *gtk.TextIter) bool {
	r.preview = false
	displayStart := cursor.Copy()
	r.view.BackwardDisplayLineStart(displayStart)
	// Keep long display lines in a viewport-sized preview.
	if cursor.Offset()-displayStart.Offset() > maxInlinePrefixRunes {
		return false
	}
	// Pango treats a zero foreground alpha as an unset alpha. Including the
	// document prefix in this layout would paint it a second time.
	r.composed = suggestion

	startRect := r.view.IterLocation(displayStart)
	caret, _ := r.view.CursorLocations(cursor)
	visible := r.view.VisibleRect()
	r.width = visible.X() + visible.Width() - caret.X() - r.view.RightMargin()
	// A right-to-left paragraph or a full line has no safe space to its
	// right. Tabs need the paragraph's tab origin, so preview them as well.
	if caret.X() < startRect.X() || r.width <= 0 || strings.ContainsRune(suggestion, '\t') {
		return false
	}
	if width, _ := r.layout().PixelSize(); width > r.width {
		return false
	}
	r.place(caret.X(), caret.Y())
	return true
}

func (r *inlineCompletionRenderer) showPreview(suggestion string, cursor, lineEnd *gtk.TextIter) {
	r.preview = true
	r.composed = "↳ " + strings.TrimLeft(suggestion, "\r\n")

	lineStart := cursor.Copy()
	lineStart.SetLineOffset(0)
	startRect := r.view.IterLocation(lineStart)
	endRect := r.view.IterLocation(lineEnd)
	visible := r.view.VisibleRect()
	x := max(startRect.X(), visible.X()+r.view.LeftMargin())
	r.width = max(1, visible.X()+visible.Width()-x-r.view.RightMargin())
	_, height := r.layout().PixelSize()
	height = max(endRect.Height(), height)
	if !r.reserveSpace(cursor, lineEnd, height) {
		r.view.SetBottomMargin(r.baseBottom + height)
		r.extendedBottom = true
	}
	r.place(x, endRect.Y()+endRect.Height())
}

func (r *inlineCompletionRenderer) place(x, y int) {
	r.viewWidth, r.viewHeight = r.view.Width(), r.view.Height()
	r.wrapMode = r.view.WrapMode()
	_, height := r.layout().PixelSize()
	r.area.SetContentWidth(r.width)
	r.area.SetContentHeight(max(1, height))
	r.view.MoveOverlay(r.area, x, y)
	r.area.SetVisible(true)
	r.area.QueueDraw()
	r.tick = r.view.AddTickCallback(func(_ gtk.Widgetter, clock gdk.FrameClocker) bool {
		r.tick = 0
		// The first tick supplies a clock even if the view was not mapped when
		// the suggestion arrived. Check after allocation, before GTK paints.
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

func (r *inlineCompletionRenderer) reserveSpace(cursor, lineEnd *gtk.TextIter, pixels int) bool {
	paragraphStart := cursor.Copy()
	paragraphStart.SetLineOffset(0)
	paragraphEnd := lineEnd.Copy()
	paragraphEnd.ForwardChar()
	if paragraphStart.Offset() == paragraphEnd.Offset() {
		return false
	}
	r.space.SetObjectProperty("pixels-below-lines", pixels)
	r.buffer.ApplyTag(r.space, paragraphStart, paragraphEnd)
	r.spaceStart = r.buffer.CreateMark("", paragraphStart, true)
	r.spaceEnd = r.buffer.CreateMark("", paragraphEnd, false)
	return true
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
	wasVisible := r.area != nil && r.area.Visible()
	if r.area != nil {
		r.area.SetVisible(false)
	}
	if r.spaceStart != nil && r.spaceEnd != nil {
		start := r.buffer.IterAtMark(r.spaceStart)
		end := r.buffer.IterAtMark(r.spaceEnd)
		r.buffer.RemoveTag(r.space, start, end)
		r.buffer.DeleteMark(r.spaceStart)
		r.buffer.DeleteMark(r.spaceEnd)
		r.spaceStart, r.spaceEnd = nil, nil
	}
	if r.extendedBottom {
		r.view.SetBottomMargin(r.baseBottom)
		r.extendedBottom = false
	}
	r.composed = ""
	r.preview = false
	if wasVisible && r.redraw != nil {
		// GtkTextView does not reliably invalidate the old pixels when a text
		// overlay is hidden and moved again in the same frame.
		r.redraw()
	}
}

func (r *inlineCompletionRenderer) close() {
	r.hide()
	if r.area != nil {
		r.area.SetDrawFunc(nil)
		r.area = nil
	}
}

func (r *inlineCompletionRenderer) layout() *pango.Layout {
	layout := r.area.CreatePangoLayout(r.composed)
	if tabs := r.view.Tabs(); tabs != nil {
		layout.SetTabs(tabs)
	}
	if r.preview {
		layout.SetWidth(r.width * pango.SCALE)
		layout.SetWrap(pango.WrapWordChar)
	} else {
		layout.SetWidth(-1)
	}

	attrs := pango.NewAttrList()
	accent := widgetColor(&r.area.Widget, "accent_color", rgbaColor{0.21, 0.52, 0.89, 1})
	ghost := pango.NewAttrForeground(color16(accent.red), color16(accent.green), color16(accent.blue))
	setAttributeRange(attrs, ghost, 0, len(r.composed))
	ghostOpacity := pango.NewAttrForegroundAlpha(color16(ghostAlpha))
	setAttributeRange(attrs, ghostOpacity, 0, len(r.composed))
	for _, span := range standaloneESpans(r.composed) {
		color := pango.NewAttrForeground(color16(accent.red), color16(accent.green), color16(accent.blue))
		setAttributeRange(attrs, color, span.byteStart, span.byteEnd)
		style := pango.NewAttrStyle(pango.StyleItalic)
		setAttributeRange(attrs, style, span.byteStart, span.byteEnd)
	}
	layout.SetAttributes(attrs)
	return layout
}

func (r *inlineCompletionRenderer) draw(_ *gtk.DrawingArea, cr *cairo.Context, width, height int) {
	// Never paint stale geometry, including before a newly mapped view has
	// installed its layout callback.
	if r.layoutChanged() {
		return
	}
	// Glyph overhangs must stay inside the suggestion's own drawing area.
	cr.Save()
	defer cr.Restore()
	cr.Rectangle(0, 0, float64(width), float64(height))
	cr.Clip()
	foreground := widgetColor(&r.area.Widget, "view_fg_color", rgbaColor{0.12, 0.12, 0.12, 1})
	cr.SetSourceRGBA(foreground.red, foreground.green, foreground.blue, foreground.alpha)
	cr.MoveTo(0, 0)
	pangocairo.ShowLayout(cr, r.layout())
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

type rgbaColor struct{ red, green, blue, alpha float64 }

func widgetColor(widget *gtk.Widget, name string, fallback rgbaColor) rgbaColor {
	// GTK4 has no replacement for resolving named theme colors.
	//nolint:staticcheck
	color, ok := widget.StyleContext().LookupColor(name)
	if !ok || color == nil {
		return fallback
	}
	return rgbaColor{
		red: float64(color.Red()), green: float64(color.Green()),
		blue: float64(color.Blue()), alpha: float64(color.Alpha()),
	}
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
