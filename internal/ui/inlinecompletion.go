package ui

import (
	"strings"
	"unicode"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotk4/pkg/pangocairo"
)

const (
	ghostAlpha               = 0.48
	maxInlineCompletionRunes = 2048
)

// inlineCompletionRenderer has two safe presentations. A single-line
// completion at the real line end is drawn beside the caret. Everything else
// is drawn in reserved space below the paragraph. Neither path masks, redraws,
// or restyles document text.
type inlineCompletionRenderer struct {
	view   *gtk.TextView
	buffer *gtk.TextBuffer
	area   *gtk.DrawingArea
	space  *gtk.TextTag

	composed      string
	prefixBytes   int
	suggestionEnd int
	width         int
	preview       bool

	spaceStart     *gtk.TextMark
	spaceEnd       *gtk.TextMark
	baseBottom     int
	extendedBottom bool
}

func newInlineCompletionRenderer(view *gtk.TextView, buffer *gtk.TextBuffer) *inlineCompletionRenderer {
	renderer := &inlineCompletionRenderer{
		view: view, buffer: buffer, baseBottom: view.BottomMargin(),
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
	return renderer
}

func (r *inlineCompletionRenderer) show(suggestion string, cursor *gtk.TextIter) bool {
	r.hide()
	if suggestion == "" {
		return false
	}

	lineEnd := cursor.Copy()
	lineEnd.ForwardToLineEnd()
	if cursor.Offset() == lineEnd.Offset() && !strings.ContainsAny(suggestion, "\r\n") {
		r.showInline(suggestion, cursor)
		return true
	}
	r.showPreview(suggestion, cursor, lineEnd)
	return true
}

func (r *inlineCompletionRenderer) showInline(suggestion string, cursor *gtk.TextIter) {
	r.preview = false
	displayStart := cursor.Copy()
	r.view.BackwardDisplayLineStart(displayStart)
	prefix := r.buffer.Text(displayStart, cursor, true)
	r.composed = prefix + suggestion
	r.prefixBytes = len(prefix)
	r.suggestionEnd = len(r.composed)

	startRect := r.view.IterLocation(displayStart)
	visible := r.view.VisibleRect()
	r.width = max(1, visible.X()+visible.Width()-startRect.X()-r.view.RightMargin())
	r.place(startRect.X(), startRect.Y())
}

func (r *inlineCompletionRenderer) showPreview(suggestion string, cursor, lineEnd *gtk.TextIter) {
	r.preview = true
	r.composed = "↳ " + strings.TrimLeft(suggestion, "\r\n")
	r.prefixBytes = 0
	r.suggestionEnd = len(r.composed)

	lineStart := cursor.Copy()
	lineStart.SetLineOffset(0)
	startRect := r.view.IterLocation(lineStart)
	endRect := r.view.IterLocation(lineEnd)
	visible := r.view.VisibleRect()
	r.width = max(120, visible.X()+visible.Width()-startRect.X()-r.view.RightMargin())
	_, height := r.layout().PixelSize()
	height = max(endRect.Height(), height)
	if !r.reserveSpace(cursor, lineEnd, height) {
		r.view.SetBottomMargin(r.baseBottom + height)
		r.extendedBottom = true
	}
	r.place(startRect.X(), endRect.Y()+endRect.Height())
}

func (r *inlineCompletionRenderer) place(x, y int) {
	_, height := r.layout().PixelSize()
	r.area.SetContentWidth(r.width)
	r.area.SetContentHeight(max(1, height))
	r.view.MoveOverlay(r.area, x, y)
	r.area.SetVisible(true)
	r.area.QueueDraw()
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
	r.prefixBytes = 0
	r.suggestionEnd = 0
	r.preview = false
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
	prefix := pango.NewAttrForegroundAlpha(0)
	setAttributeRange(attrs, prefix, 0, r.prefixBytes)
	accent := widgetColor(&r.area.Widget, "accent_color", rgbaColor{0.21, 0.52, 0.89, 1})
	ghost := pango.NewAttrForeground(color16(accent.red), color16(accent.green), color16(accent.blue))
	setAttributeRange(attrs, ghost, r.prefixBytes, r.suggestionEnd)
	ghostOpacity := pango.NewAttrForegroundAlpha(color16(ghostAlpha))
	setAttributeRange(attrs, ghostOpacity, r.prefixBytes, r.suggestionEnd)
	for _, span := range standaloneESpans(r.composed) {
		if span.byteStart < r.prefixBytes {
			continue
		}
		color := pango.NewAttrForeground(color16(accent.red), color16(accent.green), color16(accent.blue))
		setAttributeRange(attrs, color, span.byteStart, span.byteEnd)
		style := pango.NewAttrStyle(pango.StyleItalic)
		setAttributeRange(attrs, style, span.byteStart, span.byteEnd)
	}
	layout.SetAttributes(attrs)
	return layout
}

func (r *inlineCompletionRenderer) draw(_ *gtk.DrawingArea, cr *cairo.Context, _, _ int) {
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
