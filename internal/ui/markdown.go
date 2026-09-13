package ui

import (
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/jsnjack/e/internal/dispatch"
	"github.com/jsnjack/e/internal/markdownpreview"
)

const markdownPreviewDebounce = 120 * time.Millisecond

func (d *editorDocument) toggleMarkdownPreview() {
	if !isMarkdownPath(d.path) {
		d.window.toast("Markdown preview is available for .md files")
		return
	}
	if d.previewVisible {
		d.previewVisible = false
		if d.previewTimer != nil {
			d.previewTimer.Stop()
		}
		d.preview.Widget.SetVisible(false)
		d.view.Widget.GrabFocus()
		return
	}
	if d.preview == nil {
		preview, err := markdownpreview.New()
		if err != nil {
			d.window.toast("Markdown preview requires WebKitGTK 6")
			return
		}
		d.preview = preview
		d.split.SetEndChild(preview.Widget)
	}
	d.previewVisible = true
	d.preview.Widget.SetVisible(true)
	d.split.SetPosition(max(d.split.Width()/2, 320))
	d.renderMarkdownPreview()
}

func (d *editorDocument) scheduleMarkdownPreview() {
	if !d.previewVisible || d.preview == nil || d.closed {
		return
	}
	if d.previewTimer != nil {
		d.previewTimer.Stop()
	}
	d.previewTimer = time.AfterFunc(markdownPreviewDebounce, func() {
		dispatch.Main(func() {
			if d.previewVisible && !d.closed {
				d.renderMarkdownPreview()
			}
		})
	})
}

func (d *editorDocument) renderMarkdownPreview() {
	start, end := d.buffer.Bounds()
	source := []byte(d.buffer.Text(start, end, true))
	d.previewGen++
	generation := d.previewGen
	title := d.title()
	baseURI := markdownBaseURI(d.path)
	go func() {
		document, err := markdownpreview.Render(source, title)
		dispatch.Main(func() {
			if err != nil || d.closed || !d.previewVisible || generation != d.previewGen {
				return
			}
			d.preview.Load(document, baseURI)
		})
	}()
}

func isMarkdownPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	default:
		return false
	}
}

func markdownBaseURI(path string) string {
	if path == "" {
		return ""
	}
	directory := filepath.Dir(path) + string(filepath.Separator)
	return (&url.URL{Scheme: "file", Path: directory}).String()
}
