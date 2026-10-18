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
	// resumeAt makes the next refresh select the first match at or after
	// this offset, so Replace never lands inside its own replacement.
	resumeAt int
}

func newSearchState(doc *editorDocument) *searchState {
	tag := gtk.NewTextTag("e-search-match")
	tag.SetObjectProperty("background", "#f8e45c88")
	doc.buffer.TagTable().Add(tag)
	return &searchState{doc: doc, tag: tag, current: -1, resumeAt: -1}
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
	s.resumeAt = -1
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

// findExactMatches finds case-sensitive matches; with wholeWord, only matches
// with no identifier rune on either side.
func findExactMatches(text, word string, wholeWord bool) []textRange {
	pattern := regexp.QuoteMeta(word)
	matches := findRegexp(text, regexp.MustCompile(pattern))
	if !wholeWord {
		return matches
	}
	runes := []rune(text)
	kept := matches[:0]
	for _, match := range matches {
		if (match.start == 0 || !isIdentifierRune(runes[match.start-1])) &&
			(match.end == len(runes) || !isIdentifierRune(runes[match.end])) {
			kept = append(kept, match)
		}
	}
	return kept
}

func findRegexp(text string, pattern *regexp.Regexp) []textRange {
	return byteToRuneRanges(text, pattern.FindAllStringIndex(text, -1))
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
	return byteToRuneRanges(text, byteMatches)
}

func byteToRuneRanges(text string, byteMatches [][]int) []textRange {
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
	// Leave no match current until the user moves to one, so the first Find
	// Next goes to the match after the caret instead of skipping it.
	s.current = -1
	if s.resumeAt >= 0 && len(matches) > 0 {
		s.current = 0
		for index, match := range matches {
			if match.start >= s.resumeAt {
				s.current = index
				break
			}
		}
		s.resumeAt = -1
		s.selectCurrent()
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
	if s.current < 0 {
		s.doc.window.matchLabel.SetLabel(fmt.Sprintf("%d matches", len(s.matches)))
		return
	}
	s.doc.window.matchLabel.SetLabel(fmt.Sprintf("%d of %d", s.current+1, len(s.matches)))
}

func (s *searchState) navigate(direction int) {
	if len(s.matches) == 0 {
		return
	}
	if s.current < 0 {
		caret := s.doc.buffer.IterAtMark(s.doc.buffer.GetInsert()).Offset()
		if direction > 0 {
			s.current = 0
			for index, match := range s.matches {
				if match.start >= caret {
					s.current = index
					break
				}
			}
		} else {
			s.current = len(s.matches) - 1
			for index := len(s.matches) - 1; index >= 0; index-- {
				if s.matches[index].end <= caret {
					s.current = index
					break
				}
			}
		}
	} else {
		s.current = (s.current + direction + len(s.matches)) % len(s.matches)
	}
	s.selectCurrent()
	s.updateLabel()
}

func (s *searchState) selectCurrent() {
	match := s.matches[s.current]
	s.doc.buffer.SelectRange(s.doc.buffer.IterAtOffset(match.end), s.doc.buffer.IterAtOffset(match.start))
	s.doc.view.Widget.ScrollToMark(s.doc.buffer.GetInsert(), .15, false, 0, 0)
}

// replaceCurrent replaces the selected match. With no match selected, it
// first selects the next one, so a replacement is never made out of view.
func (s *searchState) replaceCurrent(replacement string) {
	if len(s.matches) == 0 {
		return
	}
	if s.current < 0 {
		s.navigate(1)
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
	// The old offsets are stale now; drop them until the refresh lands.
	s.matches, s.current = nil, -1
	s.resumeAt = match.start + utf8.RuneCountInString(replacement)
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
	// bulkEdit skips the per-change completion, preview, and timer work;
	// afterMultipleEdit does it once at the end.
	s.doc.bulkEdit = true
	s.doc.buffer.BeginUserAction()
	for index := len(s.matches) - 1; index >= 0; index-- {
		match := s.matches[index]
		start := s.doc.buffer.IterAtOffset(match.start)
		end := s.doc.buffer.IterAtOffset(match.end)
		s.doc.buffer.Delete(start, end)
		s.doc.buffer.Insert(s.doc.buffer.IterAtOffset(match.start), replacement)
	}
	s.doc.buffer.EndUserAction()
	s.doc.bulkEdit = false
	s.replacing = false
	s.doc.window.toast(fmt.Sprintf("Replaced %d matches", len(s.matches)))
	s.doc.dismissCompletion()
	s.doc.afterMultipleEdit()
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

// searchSeed returns the text a search starts with, as in VS Code: the
// selection when it is on one line, else, with nearestWord, the word at the
// caret.
func (d *editorDocument) searchSeed(nearestWord bool) string {
	start, end, selected := d.buffer.SelectionBounds()
	if selected && start.Line() != end.Line() || !selected && !nearestWord {
		return ""
	}
	word, _, _, _ := d.selectedWord()
	return word
}

// selectSpan selects from..to with the caret at the end, and scrolls it
// into view.
func (d *editorDocument) selectSpan(from, to textPosition) {
	start, _ := d.buffer.IterAtLineOffset(from.line, from.column)
	end, _ := d.buffer.IterAtLineOffset(to.line, to.column)
	d.buffer.SelectRange(end, start)
	d.view.Widget.ScrollToMark(d.buffer.GetInsert(), .15, false, 0, 0)
}
