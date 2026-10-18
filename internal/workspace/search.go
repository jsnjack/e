package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

const (
	// maxSearchFileSize skips generated bundles and data dumps, which hold
	// no hand-written text and would slow every search down.
	maxSearchFileSize = 10 << 20
	// binaryProbeSize is how much of a file is checked for a NUL byte, as
	// Git and ripgrep do, to skip binary files.
	binaryProbeSize = 8000
	// Matched lines are cut short around the match for display.
	maxContextBefore = 40
	maxContextAfter  = 200
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Query is a text search across project files, with VS Code's three
// options.
type Query struct {
	Text      string
	MatchCase bool
	WholeWord bool
	Regex     bool
}

// Compile returns the regular expression for q. It fails only when a
// Regex query is not a valid expression.
func (q Query) Compile() (*regexp.Regexp, error) {
	pattern := q.Text
	if !q.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	switch {
	case q.WholeWord && q.Regex:
		pattern = `\b(?:` + pattern + `)\b`
	case q.WholeWord:
		// A literal query only needs a boundary at an end that is a word
		// character; "-flag" still matches in "a -flag".
		if first, _ := utf8.DecodeRuneInString(q.Text); isWordRune(first) {
			pattern = `\b` + pattern
		}
		if last, _ := utf8.DecodeLastRuneInString(q.Text); isWordRune(last) {
			pattern += `\b`
		}
	}
	// ^ and $ match at each line, as in VS Code.
	flags := "(?m"
	if !q.MatchCase {
		flags += "i"
	}
	return regexp.Compile(flags + ")" + pattern)
}

// isWordRune matches the characters that Go's \b treats as word characters.
func isWordRune(r rune) bool {
	return r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// Match is one occurrence of a query in a file. Lines and columns count
// from zero, and columns count characters, as GTK text iterators do.
type Match struct {
	// Path is relative to the searched root.
	Path      string
	Line      int
	Column    int
	EndLine   int
	EndColumn int
	// Before, Text, and After are the matched line split around the match,
	// cut short for display. Text holds only the first line of a match
	// that spans lines.
	Before string
	Text   string
	After  string
}

// SearchResult holds the matches of one search in file order.
type SearchResult struct {
	Matches []Match
	// Files counts the files with at least one match.
	Files int
	// Truncated reports that the limit stopped the search early.
	Truncated bool
}

// Search finds re in the files beneath root that Files lists. Files are read
// in parallel, and the search stops after limit matches. Binary files and
// files over 10 MB are skipped.
func Search(ctx context.Context, root string, re *regexp.Regexp, limit int, hidden bool) (SearchResult, error) {
	files, err := Files(ctx, root, 0, hidden)
	if err != nil {
		return SearchResult{}, err
	}
	// Workers claim files in order, so the files below next are all read
	// when they stop and the result keeps file order.
	found := make([][]Match, len(files))
	var next, total atomic.Int64
	var wg sync.WaitGroup
	for range min(runtime.NumCPU(), 8) {
		wg.Go(func() {
			for ctx.Err() == nil && total.Load() < int64(limit) {
				index := int(next.Add(1) - 1)
				if index >= len(files) {
					return
				}
				found[index] = searchFile(root, files[index], re, limit)
				total.Add(int64(len(found[index])))
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	result := SearchResult{Truncated: int(next.Load()) < len(files)}
	for _, matches := range found {
		if len(matches) == 0 {
			continue
		}
		room := limit - len(result.Matches)
		if room <= 0 {
			result.Truncated = true
			break
		}
		if len(matches) > room {
			matches = matches[:room]
			result.Truncated = true
		}
		result.Matches = append(result.Matches, matches...)
		result.Files++
	}
	return result, nil
}

// searchFile returns up to limit matches of re in one file.
func searchFile(root, relative string, re *regexp.Regexp, limit int) []Match {
	path := filepath.Join(root, relative)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSearchFileSize {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// The editor drops the byte order mark, so columns on the first line
	// would be one character off with it.
	data = bytes.TrimPrefix(data, utf8BOM)
	if bytes.IndexByte(data[:min(len(data), binaryProbeSize)], 0) >= 0 {
		return nil
	}
	var matches []Match
	lines := lineCounter{data: data}
	for _, loc := range re.FindAllIndex(data, -1) {
		start, end := loc[0], loc[1]
		if start == end {
			// An expression such as "a*" also matches empty text everywhere.
			continue
		}
		match := Match{Path: relative}
		match.Line, match.Column = lines.position(start)
		lineStart := lines.lineStart
		lineEnd := len(data)
		if newline := bytes.IndexByte(data[start:], '\n'); newline >= 0 {
			lineEnd = start + newline
		}
		match.EndLine, match.EndColumn = lines.position(end)
		match.Before, match.Text, match.After = matchContext(data, lineStart, start, min(end, lineEnd), lineEnd)
		matches = append(matches, match)
		if len(matches) >= limit {
			break
		}
	}
	return matches
}

// lineCounter turns increasing byte offsets into line and column numbers,
// scanning the text once.
type lineCounter struct {
	data      []byte
	line      int
	lineStart int
	counted   int
}

func (c *lineCounter) position(offset int) (line, column int) {
	for {
		newline := bytes.IndexByte(c.data[c.counted:offset], '\n')
		if newline < 0 {
			break
		}
		c.line++
		c.lineStart = c.counted + newline + 1
		c.counted = c.lineStart
	}
	c.counted = offset
	return c.line, utf8.RuneCount(c.data[c.lineStart:offset])
}

// matchContext splits a line around a match. Leading indentation is dropped,
// and long text on either side is cut short with an ellipsis.
func matchContext(data []byte, lineStart, start, end, lineEnd int) (before, text, after string) {
	before = strings.TrimLeft(string(data[lineStart:start]), " \t")
	if len(before) > maxContextBefore {
		cut := len(before) - maxContextBefore
		for cut < len(before) && !utf8.RuneStart(before[cut]) {
			cut++
		}
		before = "…" + before[cut:]
	}
	text = cutAfter(string(data[start:end]), maxContextAfter)
	after = strings.TrimRight(string(data[end:lineEnd]), "\r")
	after = cutAfter(after, maxContextAfter-len(text))
	return strings.ToValidUTF8(before, "�"), strings.ToValidUTF8(text, "�"), strings.ToValidUTF8(after, "�")
}

// cutAfter keeps at most limit bytes of text, ending on a whole character.
func cutAfter(text string, limit int) string {
	if len(text) <= max(limit, 0) {
		return text
	}
	cut := max(limit, 0)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}
