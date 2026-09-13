package ui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/dispatch"
)

const maxHighlightedMatches = 2000

type textRange struct {
	start int
	end   int
}

type searchState struct {
	doc        *editorDocument
	query      string
	matches    []textRange
	current    int
	tag        *gtk.TextTag
	generation atomic.Uint64
	timer      *time.Timer
}

func newSearchState(doc *editorDocument) *searchState {
	tag := gtk.NewTextTag("e-search-match")
	tag.SetObjectProperty("background", "#f8e45c88")
	doc.buffer.TagTable().Add(tag)
	return &searchState{doc: doc, tag: tag, current: -1}
}

func (s *searchState) update(query string) {
	s.query = query
	s.scheduleRefresh()
}

func (s *searchState) scheduleRefresh() {
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(80*time.Millisecond, func() {
		dispatch.Main(func() {
			if !s.doc.closed {
				s.refresh()
			}
		})
	})
}

func (s *searchState) refresh() {
	gen := s.generation.Add(1)
	query := s.query
	start, end := s.doc.buffer.Bounds()
	text := s.doc.buffer.Text(start, end, true)
	if query == "" {
		s.apply(gen, nil)
		return
	}
	go func() {
		matches := findMatches(text, query)
		dispatch.Main(func() { s.apply(gen, matches) })
	}()
}

func findMatches(text, query string) []textRange {
	if query == "" {
		return nil
	}
	haystack, needle := text, query
	if query == strings.ToLower(query) {
		haystack, needle = strings.ToLower(text), strings.ToLower(query)
	}
	var matches []textRange
	byteBase, runeBase := 0, 0
	for byteBase <= len(haystack) {
		index := strings.Index(haystack[byteBase:], needle)
		if index < 0 {
			break
		}
		prefix := haystack[byteBase : byteBase+index]
		start := runeBase + utf8.RuneCountInString(prefix)
		length := utf8.RuneCountInString(haystack[byteBase+index : byteBase+index+len(needle)])
		matches = append(matches, textRange{start: start, end: start + length})
		advance := index + len(needle)
		runeBase += utf8.RuneCountInString(haystack[byteBase : byteBase+advance])
		byteBase += advance
	}
	return matches
}

func (s *searchState) apply(generation uint64, matches []textRange) {
	if generation != s.generation.Load() || s.doc.closed {
		return
	}
	start, end := s.doc.buffer.Bounds()
	s.doc.buffer.RemoveTag(s.tag, start, end)
	s.matches = matches
	for index, match := range matches {
		if index >= maxHighlightedMatches {
			break
		}
		s.doc.buffer.ApplyTag(s.tag, s.doc.buffer.IterAtOffset(match.start), s.doc.buffer.IterAtOffset(match.end))
	}
	if len(matches) == 0 {
		s.current = -1
	} else if s.current < 0 || s.current >= len(matches) {
		s.current = 0
	}
	s.updateLabel()
}

func (s *searchState) updateLabel() {
	if s.doc.window.active() != s.doc {
		return
	}
	if len(s.matches) == 0 {
		if s.query == "" {
			s.doc.window.matchLabel.SetLabel("")
		} else {
			s.doc.window.matchLabel.SetLabel("No matches")
		}
		return
	}
	s.doc.window.matchLabel.SetLabel(fmt.Sprintf("%d of %d", s.current+1, len(s.matches)))
}

func (s *searchState) navigate(direction int) {
	if len(s.matches) == 0 {
		return
	}
	s.current = (s.current + direction + len(s.matches)) % len(s.matches)
	match := s.matches[s.current]
	start := s.doc.buffer.IterAtOffset(match.start)
	end := s.doc.buffer.IterAtOffset(match.end)
	s.doc.buffer.SelectRange(end, start)
	s.doc.view.Widget.ScrollToIter(start, .15, false, 0, 0)
	s.updateLabel()
}

func (s *searchState) replaceCurrent(replacement string) {
	if len(s.matches) == 0 || s.current < 0 {
		return
	}
	match := s.matches[s.current]
	s.doc.buffer.BeginUserAction()
	start := s.doc.buffer.IterAtOffset(match.start)
	end := s.doc.buffer.IterAtOffset(match.end)
	s.doc.buffer.Delete(start, end)
	s.doc.buffer.Insert(s.doc.buffer.IterAtOffset(match.start), replacement)
	s.doc.buffer.EndUserAction()
	s.refresh()
}

func (s *searchState) replaceAll(replacement string) {
	if len(s.matches) == 0 {
		return
	}
	s.doc.buffer.BeginUserAction()
	for index := len(s.matches) - 1; index >= 0; index-- {
		match := s.matches[index]
		start := s.doc.buffer.IterAtOffset(match.start)
		end := s.doc.buffer.IterAtOffset(match.end)
		s.doc.buffer.Delete(start, end)
		s.doc.buffer.Insert(s.doc.buffer.IterAtOffset(match.start), replacement)
	}
	s.doc.buffer.EndUserAction()
	s.doc.window.toast(fmt.Sprintf("Replaced %d matches", len(s.matches)))
	s.refresh()
}
