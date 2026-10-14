package editorconfig

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// maxCachedGlobs bounds the compiled pattern cache. Patterns come from
// .editorconfig files, so the cache only fills when many projects are open.
const maxCachedGlobs = 512

// glob is a section pattern compiled to a regular expression. Each capture
// group in re belongs to one {num1..num2} range, in order.
type glob struct {
	re     *regexp.Regexp
	ranges []numRange
}

type numRange struct{ lo, hi int }

var (
	globMu    sync.Mutex
	globCache = make(map[string]*glob)
)

var rangePattern = regexp.MustCompile(`^([+-]?[0-9]+)\.\.([+-]?[0-9]+)$`)

// matches reports whether the section pattern matches rel, the slash-separated
// path of the file relative to the .editorconfig directory.
func matches(pattern, rel string) bool {
	g := compileGlob(pattern)
	if g == nil {
		return false
	}
	groups := g.re.FindStringSubmatchIndex("/" + rel)
	if groups == nil {
		return false
	}
	target := "/" + rel
	for i, r := range g.ranges {
		start, end := groups[2*i+2], groups[2*i+3]
		if start < 0 {
			continue
		}
		n, err := strconv.Atoi(target[start:end])
		if err != nil || n < r.lo || n > r.hi {
			return false
		}
	}
	return true
}

// compileGlob translates an EditorConfig glob and caches the result. It
// returns nil for a pattern that does not compile.
func compileGlob(pattern string) *glob {
	globMu.Lock()
	defer globMu.Unlock()
	if g, ok := globCache[pattern]; ok {
		return g
	}
	if len(globCache) >= maxCachedGlobs {
		clear(globCache)
	}
	var g *glob
	var ranges []numRange
	expr := translateGlob(anchorGlob(pattern), &ranges)
	if re, err := regexp.Compile("^" + expr + "$"); err == nil {
		g = &glob{re: re, ranges: ranges}
	}
	globCache[pattern] = g
	return g
}

// anchorGlob applies the spec's path rule. A pattern with a slash is relative
// to the .editorconfig directory. A pattern without one matches at any depth.
// The result always starts with "/" and is matched against "/" + rel.
func anchorGlob(pattern string) string {
	if strings.Contains(pattern, "/") {
		return "/" + strings.TrimPrefix(pattern, "/")
	}
	return "/**/" + pattern
}

// translateGlob converts glob syntax to RE2 syntax and appends one numRange
// per capture group it emits.
func translateGlob(pattern string, ranges *[]numRange) string {
	var out strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '\\':
			if i+1 < len(pattern) {
				i++
				out.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
			} else {
				out.WriteString(`\\`)
			}
		case '/':
			if strings.HasPrefix(pattern[i:], "/**/") {
				out.WriteString("(?:/|/.*/)")
				i += 3
			} else {
				out.WriteByte('/')
			}
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				out.WriteString(".*")
				i++
			} else {
				out.WriteString("[^/]*")
			}
		case '?':
			out.WriteString("[^/]")
		case '[':
			class, next, ok := translateClass(pattern, i)
			if !ok {
				out.WriteString(`\[`)
				continue
			}
			out.WriteString(class)
			i = next
		case '{':
			end := matchingBrace(pattern, i)
			if end < 0 {
				out.WriteString(`\{`)
				continue
			}
			out.WriteString(translateBraces(pattern[i+1:end], ranges))
			i = end
		default:
			out.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	return out.String()
}

// translateClass converts [name] or [!name] starting at pattern[start]. It
// returns the index of the closing bracket. A class containing "/" or without
// a closing bracket is not a class, and the "[" is literal.
func translateClass(pattern string, start int) (string, int, bool) {
	i := start + 1
	negate := i < len(pattern) && pattern[i] == '!'
	if negate {
		i++
	}
	var body strings.Builder
	for first := true; i < len(pattern); i, first = i+1, false {
		c := pattern[i]
		switch {
		case c == ']' && !first:
			if body.Len() == 0 {
				return "", 0, false
			}
			if negate {
				return "[^/" + body.String() + "]", i, true
			}
			return "[" + body.String() + "]", i, true
		case c == '/':
			return "", 0, false
		case c == '\\' && i+1 < len(pattern):
			i++
			body.WriteString(classChar(pattern[i]))
		case c == '-' && !first && i+1 < len(pattern) && pattern[i+1] != ']':
			body.WriteByte('-')
		default:
			body.WriteString(classChar(c))
		}
	}
	return "", 0, false
}

func classChar(c byte) string {
	switch c {
	case '\\', '[', ']', '^', '-':
		return `\` + string(c)
	}
	return string(c)
}

// matchingBrace returns the index of the "}" closing the "{" at start, or -1.
func matchingBrace(pattern string, start int) int {
	depth := 0
	for i := start; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// translateBraces converts the body of a {...} group: a numeric range, a
// comma-separated choice with nested groups, or a literal single word.
func translateBraces(body string, ranges *[]numRange) string {
	if m := rangePattern.FindStringSubmatch(body); m != nil {
		lo, errLo := strconv.Atoi(m[1])
		hi, errHi := strconv.Atoi(m[2])
		if errLo == nil && errHi == nil {
			if lo > hi {
				lo, hi = hi, lo
			}
			*ranges = append(*ranges, numRange{lo: lo, hi: hi})
			return `([+-]?[0-9]+)`
		}
	}
	parts := splitTopLevel(body)
	if len(parts) == 1 {
		return `\{` + translateGlob(body, ranges) + `\}`
	}
	choices := make([]string, len(parts))
	for i, part := range parts {
		choices[i] = translateGlob(part, ranges)
	}
	return "(?:" + strings.Join(choices, "|") + ")"
}

// splitTopLevel splits body at commas outside nested braces and escapes.
func splitTopLevel(body string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, body[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, body[start:])
}
