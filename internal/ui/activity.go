package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/jsnjack/e/internal/activity"
	"github.com/jsnjack/e/internal/dispatch"
)

const (
	activityHistoryLimit = 100
	activityLingerMS     = 3000
	activityTickMS       = 150
)

type runningActivity struct {
	event   activity.Event
	started time.Time
	row     *activityLogRow
}

type activityLogRow struct {
	box      *gtk.Box
	icon     *gtk.Image
	label    *gtk.Label
	detail   *gtk.Label
	duration *gtk.Label
	started  time.Time
}

type activitySurface struct {
	hub *activity.Hub

	bar             *gtk.Box
	document        *gtk.Label
	cursor          *gtk.Label
	model           *gtk.Label
	button          *gtk.MenuButton
	popover         *gtk.Popover
	icons           *gtk.Stack
	doneIcon        *gtk.Image
	label           *gtk.Label
	detail          *gtk.Label
	fallback        *gtk.Label
	progress        *gtk.ProgressBar
	logBox          *gtk.Box
	logEmpty        *gtk.Label
	serving         *gtk.Label
	servingChip     *gtk.Label
	servingModel    string
	servingFallback bool

	running map[uint64]*runningActivity
	order   []uint64
	history []*activityLogRow
	finish  *activity.Event
	tick    glib.SourceHandle
	linger  glib.SourceHandle

	destroyed bool
}

func newActivitySurface(hub *activity.Hub) *activitySurface {
	s := &activitySurface{hub: hub, running: make(map[uint64]*runningActivity)}

	s.button = gtk.NewMenuButton()
	s.button.SetIconName("view-list-symbolic")
	s.button.SetTooltipText("Activity")
	s.button.AddCSSClass("flat")
	s.popover = s.buildPopover()
	s.button.SetPopover(s.popover)

	spinner := adw.NewSpinner()
	spinner.SetSizeRequest(16, 16)
	s.doneIcon = gtk.NewImageFromIconName("object-select-symbolic")
	s.doneIcon.SetPixelSize(16)
	s.icons = gtk.NewStack()
	s.icons.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	s.icons.SetTransitionDuration(250)
	s.icons.AddNamed(spinner, "running")
	s.icons.AddNamed(s.doneIcon, "done")
	s.icons.SetSizeRequest(16, 16)
	s.icons.SetOpacity(0)

	s.label = gtk.NewLabel("")
	s.label.SetXAlign(0)
	s.label.SetEllipsize(pango.EllipsizeEnd)
	s.label.SetMaxWidthChars(22)
	s.detail = gtk.NewLabel("")
	s.detail.AddCSSClass("activity-detail")
	s.detail.SetXAlign(0)
	s.detail.SetMaxWidthChars(10)
	s.fallback = gtk.NewLabel("fallback")
	s.fallback.AddCSSClass("activity-chip")
	s.fallback.SetVisible(false)

	s.progress = gtk.NewProgressBar()
	s.progress.AddCSSClass("activity-progress")
	s.progress.SetVisible(false)
	s.progress.SetSizeRequest(72, -1)

	s.document = gtk.NewLabel("New · Plain Text")
	s.document.SetXAlign(0)
	s.document.SetHExpand(true)
	s.document.SetEllipsize(pango.EllipsizeEnd)
	s.cursor = gtk.NewLabel("Ln 1, Col 1")
	s.cursor.AddCSSClass("status-position")
	s.cursor.SetWidthChars(13)
	s.cursor.SetXAlign(1)
	s.model = gtk.NewLabel("AI: —")
	s.model.AddCSSClass("status-model")
	s.model.SetMaxWidthChars(18)
	s.model.SetEllipsize(pango.EllipsizeEnd)

	status := gtk.NewBox(gtk.OrientationHorizontal, 7)
	status.SetHAlign(gtk.AlignEnd)
	status.Append(s.icons)
	status.Append(s.label)
	status.Append(s.detail)
	status.Append(s.progress)
	status.Append(s.model)
	status.Append(s.fallback)
	status.Append(s.cursor)
	status.Append(s.button)

	s.bar = gtk.NewBox(gtk.OrientationHorizontal, 8)
	s.bar.AddCSSClass("status-bar")
	s.bar.Append(s.document)
	s.bar.Append(status)
	return s
}

func (s *activitySurface) buildPopover() *gtk.Popover {
	title := gtk.NewLabel("Activity")
	title.SetXAlign(0)
	title.AddCSSClass("heading")

	s.logBox = gtk.NewBox(gtk.OrientationVertical, 2)
	s.logEmpty = gtk.NewLabel("No activity yet")
	s.logEmpty.SetXAlign(0)
	s.logEmpty.AddCSSClass("dim-label")
	s.logBox.Append(s.logEmpty)

	scroller := gtk.NewScrolledWindow()
	scroller.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroller.SetMaxContentHeight(320)
	scroller.SetPropagateNaturalHeight(true)
	scroller.SetChild(s.logBox)

	servingKey := gtk.NewLabel("Serving")
	servingKey.AddCSSClass("dim-label")
	s.serving = gtk.NewLabel("—")
	s.serving.SetEllipsize(pango.EllipsizeEnd)
	s.serving.SetHExpand(true)
	s.serving.SetXAlign(0)
	s.servingChip = gtk.NewLabel("fallback")
	s.servingChip.AddCSSClass("activity-chip")
	s.servingChip.SetVisible(false)
	servingLine := gtk.NewBox(gtk.OrientationHorizontal, 8)
	servingLine.Append(servingKey)
	servingLine.Append(s.serving)
	servingLine.Append(s.servingChip)

	content := gtk.NewBox(gtk.OrientationVertical, 8)
	content.SetMarginTop(10)
	content.SetMarginBottom(10)
	content.SetMarginStart(10)
	content.SetMarginEnd(10)
	content.SetSizeRequest(440, -1)
	content.Append(title)
	content.Append(scroller)
	content.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	content.Append(servingLine)

	popover := gtk.NewPopover()
	popover.SetChild(content)
	return popover
}

func (s *activitySurface) mount(toolbar *adw.ToolbarView) { toolbar.AddBottomBar(s.bar) }

func (s *activitySurface) updateDocument(doc *editorDocument) {
	if doc == nil {
		s.document.SetText("No document")
		s.document.SetTooltipText("")
		s.cursor.SetText("Ln —, Col —")
		return
	}
	state := ""
	if doc.path == "" {
		state = "New"
	}
	if doc.buffer.Modified() {
		state = "Modified"
	}
	language := statusLanguage(languageForPath(doc.path))
	newline := strings.ToUpper(string(doc.newline))
	if newline == "" {
		newline = "LF"
	}
	parts := []string{language}
	if state != "" {
		parts = append([]string{state}, parts...)
	}
	s.document.SetText(strings.Join(parts, " · "))
	details := []string{"Saved", language, "UTF-8", newline}
	if state != "" {
		details[0] = state
	}
	if doc.path != "" {
		details = append(details, doc.path)
	}
	s.document.SetTooltipText(strings.Join(details, " · "))
	insert := doc.buffer.IterAtMark(doc.buffer.GetInsert())
	s.cursor.SetText(fmt.Sprintf("Ln %d, Col %d", insert.Line()+1, insert.LineOffset()+1))
}

func statusLanguage(language string) string {
	if language == "" {
		return "Plain Text"
	}
	if label := map[string]string{
		"cpp": "C++", "css": "CSS", "go": "Go", "html": "HTML",
		"javascript": "JavaScript", "json": "JSON", "markdown": "Markdown",
		"python": "Python", "rust": "Rust", "shellscript": "Shell",
		"typescript": "TypeScript", "yaml": "YAML",
	}[language]; label != "" {
		return label
	}
	return language
}

func (s *activitySurface) subscribe(ctx context.Context) {
	events, unsubscribe := s.hub.Subscribe()
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				dispatch.Main(func() {
					if !s.destroyed {
						s.feed(event)
					}
				})
			}
		}
	}()
}

func (s *activitySurface) feed(event activity.Event) {
	s.button.SetTooltipText("Activity · " + event.Label)
	switch event.Phase {
	case activity.Started:
		running := &runningActivity{event: event, started: event.At}
		if running.started.IsZero() {
			running.started = time.Now()
		}
		running.row = s.newLogRow(event, running.started)
		s.running[event.ID] = running
		s.order = append(s.order, event.ID)
		s.finish = nil
		s.repaint()
	case activity.Progressed:
		if running := s.running[event.ID]; running != nil {
			running.event.Current, running.event.Total = event.Current, event.Total
			running.row.duration.SetText(fmt.Sprintf("%d/%d", event.Current, event.Total))
			s.repaint()
		}
	case activity.Cancelled:
		s.removeRunning(event.ID, true)
		s.repaint()
	case activity.Finished, activity.Failed:
		running := s.removeRunning(event.ID, false)
		if running == nil {
			running = &runningActivity{event: event, started: event.At, row: s.newLogRow(event, event.At)}
		}
		s.finishLogRow(running.row, event, time.Since(running.started))
		if event.Model != "" {
			s.setServing(event.Model, event.Fallback)
		}
		s.finish = &event
		s.repaint()
	}
}

func (s *activitySurface) removeRunning(id uint64, removeLog bool) *runningActivity {
	running := s.running[id]
	if running == nil {
		return nil
	}
	delete(s.running, id)
	for index, candidate := range s.order {
		if candidate == id {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	if removeLog && running.row != nil {
		s.logBox.Remove(running.row.box)
		for index, row := range s.history {
			if row == running.row {
				s.history = append(s.history[:index], s.history[index+1:]...)
				break
			}
		}
		s.logEmpty.SetVisible(len(s.history) == 0)
	}
	return running
}

func (s *activitySurface) repaint() {
	s.unscheduleLinger()
	if running, visibleCount := s.latestVisibleRunning(); running != nil {
		s.icons.SetVisibleChildName("running")
		label, detail := running.event.Label, shortDuration(time.Since(running.started))
		if extra := visibleCount - 1; extra > 0 {
			label += fmt.Sprintf(" (+%d more)", extra)
		}
		s.label.SetText(label)
		s.detail.SetText(detail)
		s.icons.SetOpacity(1)
		s.fallback.SetVisible(s.servingFallback)
		bounded := running.event.Total > 0
		s.progress.SetVisible(bounded)
		if bounded {
			s.progress.SetFraction(float64(running.event.Current) / float64(running.event.Total))
		}
		s.startTick()
		return
	}
	s.stopTick()
	s.progress.SetVisible(false)
	if s.finish == nil {
		s.icons.SetOpacity(0)
		s.label.SetText("")
		s.detail.SetText("")
		s.fallback.SetVisible(s.servingFallback)
		s.refreshButtonState()
		return
	}
	event := *s.finish
	s.icons.SetVisibleChildName("done")
	s.icons.SetOpacity(1)
	if event.Phase == activity.Failed {
		s.doneIcon.SetFromIconName("process-stop-symbolic")
		s.doneIcon.AddCSSClass("error")
	} else {
		s.doneIcon.SetFromIconName("object-select-symbolic")
		s.doneIcon.RemoveCSSClass("error")
	}
	s.label.SetText(activityResultLabel(event))
	s.detail.SetText(activityResultDetail(event))
	s.fallback.SetVisible(event.Fallback || s.servingFallback)
	s.linger = glib.TimeoutAdd(activityLingerMS, func() bool {
		s.linger = 0
		s.finish = nil
		s.repaint()
		return false
	})
}

func (s *activitySurface) latestVisibleRunning() (*runningActivity, int) {
	var latest *runningActivity
	count := 0
	for _, id := range s.order {
		running := s.running[id]
		if running == nil {
			continue
		}
		latest = running
		count++
	}
	return latest, count
}

func (s *activitySurface) startTick() {
	if s.tick != 0 {
		return
	}
	s.tick = glib.TimeoutAdd(activityTickMS, func() bool {
		if s.destroyed || len(s.order) == 0 {
			s.tick = 0
			return false
		}
		s.repaint()
		return true
	})
}

func (s *activitySurface) stopTick() {
	if s.tick != 0 {
		glib.SourceRemove(s.tick)
		s.tick = 0
	}
}

func (s *activitySurface) unscheduleLinger() {
	if s.linger != 0 {
		glib.SourceRemove(s.linger)
		s.linger = 0
	}
}

func (s *activitySurface) setServing(model string, fallback bool) {
	s.servingModel = model
	s.servingFallback = fallback
	s.serving.SetText(model)
	s.model.SetText("AI: " + model)
	s.servingChip.SetVisible(fallback)
	s.fallback.SetVisible(fallback)
	s.refreshButtonState()
}

func (s *activitySurface) refreshButtonState() {
	if s.servingModel == "" {
		s.button.SetTooltipText("Activity")
		return
	}
	state := "Activity · Serving " + s.servingModel
	if s.servingFallback {
		state += " (fallback)"
	}
	s.button.SetTooltipText(state)
}

func (s *activitySurface) newLogRow(event activity.Event, started time.Time) *activityLogRow {
	row := &activityLogRow{
		icon:  gtk.NewImageFromIconName("media-playback-start-symbolic"),
		label: gtk.NewLabel(event.Label), detail: gtk.NewLabel(""),
		duration: gtk.NewLabel("…"), started: started,
	}
	row.icon.SetPixelSize(14)
	row.label.SetXAlign(0)
	row.label.SetEllipsize(pango.EllipsizeEnd)
	row.label.SetHExpand(true)
	row.detail.SetXAlign(0)
	row.detail.SetEllipsize(pango.EllipsizeEnd)
	row.detail.AddCSSClass("activity-log-detail")
	row.detail.SetVisible(false)
	row.duration.AddCSSClass("dim-label")
	row.duration.SetVAlign(gtk.AlignStart)

	text := gtk.NewBox(gtk.OrientationVertical, 1)
	text.Append(row.label)
	text.Append(row.detail)
	row.box = gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.box.AddCSSClass("activity-log-row")
	row.box.Append(row.icon)
	row.box.Append(text)
	row.box.Append(row.duration)
	s.logBox.Prepend(row.box)
	s.logEmpty.SetVisible(false)
	s.history = append([]*activityLogRow{row}, s.history...)
	if len(s.history) > activityHistoryLimit {
		oldest := s.history[len(s.history)-1]
		s.logBox.Remove(oldest.box)
		s.history = s.history[:len(s.history)-1]
	}
	return row
}

func (s *activitySurface) finishLogRow(row *activityLogRow, event activity.Event, elapsed time.Duration) {
	if event.Phase == activity.Failed {
		row.icon.SetFromIconName("process-stop-symbolic")
		row.icon.AddCSSClass("error")
	} else {
		row.icon.SetFromIconName("object-select-symbolic")
	}
	row.label.SetText(activityResultLabel(event))
	detail := activityLogDetail(event)
	row.detail.SetText(detail)
	row.detail.SetVisible(detail != "")
	row.duration.SetText(shortDuration(elapsed))
}

func activityLogDetail(event activity.Event) string {
	detail := activityResultDetail(event)
	if event.Fallback && event.Model != "" {
		detail = strings.TrimSuffix(detail, event.Model) + event.Model + " (fallback)"
	}
	return detail
}

func (s *activitySurface) destroy() {
	s.destroyed = true
	s.stopTick()
	s.unscheduleLinger()
}

func activityResultLabel(event activity.Event) string {
	if event.Phase == activity.Failed {
		for _, wording := range []struct{ current, failed string }{
			{"Opening ", "Couldn’t open "},
			{"Saving ", "Couldn’t save "},
			{"Indexing ", "Couldn’t index "},
			{"Generating suggestion", "AI suggestion failed"},
			{"Transforming selection", "AI transformation failed"},
		} {
			if strings.HasPrefix(event.Label, wording.current) {
				return wording.failed + strings.TrimPrefix(event.Label, wording.current)
			}
		}
		return event.Label + " failed"
	}
	for _, wording := range []struct{ current, finished string }{
		{"Opening ", "Opened "},
		{"Saving ", "Saved "},
		{"Indexing ", "Indexed "},
		{"Generating suggestion", "Suggestion ready"},
		{"Transforming selection", "Transformation ready"},
	} {
		if strings.HasPrefix(event.Label, wording.current) {
			return wording.finished + strings.TrimPrefix(event.Label, wording.current)
		}
	}
	return event.Label
}

func activityResultDetail(event activity.Event) string {
	parts := make([]string, 0, 2)
	if event.Note != "" {
		parts = append(parts, event.Note)
	}
	if event.Model != "" {
		parts = append(parts, event.Model)
	}
	return strings.Join(parts, " · ")
}

func shortDuration(duration time.Duration) string {
	if duration < time.Second {
		return fmt.Sprintf("%.1fs", max(0, duration.Seconds()))
	}
	if duration < time.Minute {
		return fmt.Sprintf("%.1fs", duration.Seconds())
	}
	return fmt.Sprintf("%dm %02ds", int(duration.Minutes()), int(duration.Seconds())%60)
}

func humanByteCount(size int) string {
	if size < 1024 {
		return activity.Plural(size, "byte", "bytes")
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}
