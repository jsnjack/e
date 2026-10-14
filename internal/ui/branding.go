package ui

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

const brandScanChunk = 32 * 1024

func (d *editorDocument) configureBranding() {
	d.brandTag = gtk.NewTextTag("e-brand")
	d.brandTag.SetObjectProperty("style", pango.StyleItalic)
	d.updateBrandColor()
	d.buffer.TagTable().Add(d.brandTag)
	// Follow accent and dark-mode changes instead of keeping the startup color.
	style := adw.StyleManagerGetDefault()
	d.brandStyleSignals = []coreglib.SignalHandle{
		style.NotifyProperty("accent-color", d.updateBrandColor),
		style.NotifyProperty("dark", d.updateBrandColor),
	}
}

func (d *editorDocument) updateBrandColor() {
	style := adw.StyleManagerGetDefault()
	accent := adw.AccentColorToStandaloneRGBA(style.AccentColor(), style.Dark())
	d.brandTag.SetObjectProperty("foreground", fmt.Sprintf(
		"#%02x%02x%02x", color8(float64(accent.Red())), color8(float64(accent.Green())), color8(float64(accent.Blue())),
	))
}

func (d *editorDocument) closeBranding() {
	style := adw.StyleManagerGetDefault()
	for _, handle := range d.brandStyleSignals {
		style.HandlerDisconnect(handle)
	}
	d.brandStyleSignals = nil
}

func color8(value float64) uint8 { return uint8(max(0, min(1, value))*255 + 0.5) }

// scanBranding incrementally styles standalone mentions without copying or
// scanning the entire document in one GTK frame.
func (d *editorDocument) scanBranding() {
	if d.brandTag == nil || d.closed {
		return
	}
	d.brandScanGen++
	generation := d.brandScanGen
	offset := 0
	glib.IdleAdd(func() bool {
		if d.closed || generation != d.brandScanGen {
			return false
		}
		total := d.buffer.CharCount()
		if offset >= total {
			return false
		}
		end := min(offset+brandScanChunk, total)
		d.applyBrandRange(offset, end)
		offset = end
		return offset < total
	})
}

func (d *editorDocument) scheduleBrandRange(start, end int) {
	if d.brandTag == nil || d.closed {
		return
	}
	start = max(0, start)
	end = max(start, end)
	if d.brandRefreshPending {
		d.brandRefreshStart = min(d.brandRefreshStart, start)
		d.brandRefreshEnd = max(d.brandRefreshEnd, end)
		return
	}
	d.brandRefreshPending = true
	d.brandRefreshStart, d.brandRefreshEnd = start, end
	glib.IdleAdd(func() bool {
		if d.closed {
			d.brandRefreshPending = false
			return false
		}
		end := min(d.buffer.CharCount(), d.brandRefreshEnd)
		chunkEnd := min(d.brandRefreshStart+brandScanChunk, end)
		d.applyBrandRange(d.brandRefreshStart, chunkEnd)
		d.brandRefreshStart = chunkEnd
		if d.brandRefreshStart < end {
			return true
		}
		d.brandRefreshPending = false
		return false
	})
}

func (d *editorDocument) applyBrandRange(startOffset, endOffset int) {
	if d.brandTag == nil || startOffset >= endOffset {
		return
	}
	startOffset = max(0, startOffset)
	endOffset = min(d.buffer.CharCount(), endOffset)
	start, end := d.buffer.IterAtOffset(startOffset), d.buffer.IterAtOffset(endOffset)
	d.buffer.RemoveTag(d.brandTag, start, end)

	lookupStartOffset := max(0, startOffset-1)
	lookupEndOffset := min(d.buffer.CharCount(), endOffset+1)
	lookupStart := d.buffer.IterAtOffset(lookupStartOffset)
	lookupEnd := d.buffer.IterAtOffset(lookupEndOffset)
	for _, span := range standaloneESpans(d.buffer.Text(lookupStart, lookupEnd, true)) {
		absoluteStart := lookupStartOffset + span.runeStart
		if absoluteStart < startOffset || absoluteStart >= endOffset {
			continue
		}
		d.buffer.ApplyTag(
			d.brandTag,
			d.buffer.IterAtOffset(absoluteStart),
			d.buffer.IterAtOffset(lookupStartOffset+span.runeEnd),
		)
	}
	// GtkSourceView may add language tags after this tag was created. Restoring
	// the highest priority keeps the small branding override deterministic.
	d.brandTag.SetPriority(d.buffer.TagTable().Size() - 1)
}
