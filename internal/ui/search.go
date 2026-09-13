package ui

import (
	"fmt"
	"regexp"
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
	replacing  bool
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
	s.generation.Add(1)
	start, end := s.doc.buffer.Bounds()
	s.doc.buffer.RemoveTag(s.tag, start, end)
	s.matches = nil
	s.current = -1
	s.updateLabel()
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
	if query == "" {
		s.apply(gen, nil)
		return
	}
	s.doc.textSnapshotAsync(func() bool {
		return gen == s.generation.Load() && !s.doc.closed
	}, func(text string) {
		go func() {
			matches := findMatches(text, query)
			dispatch.Main(func() { s.apply(gen, matches) })
		}()
	})
}

func findMatches(text, query string) []textRange {
	if query == "" {
		return nil
	}
	var byteMatches [][]int
	if query == strings.ToLower(query) {
		byteMatches = regexp.MustCompile("(?i:"+regexp.QuoteMeta(query)+")").FindAllStringIndex(text, -1)
	} else {
		for offset := 0; offset <= len(text); {
			index := strings.Index(text[offset:], query)
			if index < 0 {
				break
			}
			start := offset + index
			byteMatches = append(byteMatches, []int{start, start + len(query)})
			offset = start + len(query)
		}
	}
	var matches []textRange
	byteBase, runeBase := 0, 0
	for _, match := range byteMatches {
		runeBase += utf8.RuneCountInString(text[byteBase:match[0]])
		start := runeBase
		length := utf8.RuneCountInString(text[match[0]:match[1]])
		matches = append(matches, textRange{start: start, end: start + length})
		runeBase += length
		byteBase = match[1]
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
	if !s.matchIsCurrent(match) {
		s.scheduleRefresh()
		return
	}
	s.replacing = true
	s.doc.buffer.BeginUserAction()
	start := s.doc.buffer.IterAtOffset(match.start)
	end := s.doc.buffer.IterAtOffset(match.end)
	s.doc.buffer.Delete(start, end)
	s.doc.buffer.Insert(s.doc.buffer.IterAtOffset(match.start), replacement)
	s.doc.buffer.EndUserAction()
	s.replacing = false
	s.refresh()
}

func (s *searchState) replaceAll(replacement string) {
	if len(s.matches) == 0 {
		return
	}
	for _, match := range s.matches {
		if !s.matchIsCurrent(match) {
			s.scheduleRefresh()
			return
		}
	}
	s.replacing = true
	s.doc.buffer.BeginUserAction()
	for index := len(s.matches) - 1; index >= 0; index-- {
		match := s.matches[index]
		start := s.doc.buffer.IterAtOffset(match.start)
		end := s.doc.buffer.IterAtOffset(match.end)
		s.doc.buffer.Delete(start, end)
		s.doc.buffer.Insert(s.doc.buffer.IterAtOffset(match.start), replacement)
	}
	s.doc.buffer.EndUserAction()
	s.replacing = false
	s.doc.window.toast(fmt.Sprintf("Replaced %d matches", len(s.matches)))
	s.refresh()
}

func (s *searchState) matchIsCurrent(match textRange) bool {
	if match.start < 0 || match.end < match.start || match.end > s.doc.buffer.CharCount() {
		return false
	}
	text := s.doc.buffer.Text(s.doc.buffer.IterAtOffset(match.start), s.doc.buffer.IterAtOffset(match.end), true)
	if s.query == strings.ToLower(s.query) {
		return strings.EqualFold(text, s.query)
	}
	return text == s.query
}
