// Package linediff compares two texts line by line for gutter change marks.
// Lines are compared by 64-bit hash, so a caller keeps one small number per
// line instead of a second copy of the text.
package linediff

import "strings"

// maxEdits bounds the Myers search. Past it, the whole changed middle is one
// Modified hunk; the trace then needs about maxEdits² ints.
const maxEdits = 1500

// Kind is how a hunk changed the new text.
type Kind int

const (
	// Added lines are new.
	Added Kind = iota + 1
	// Modified lines replace old lines.
	Modified
	// Deleted marks old lines removed just before Line; Count is zero.
	Deleted
)

// Hunk is one run of changed lines, in 0-based line numbers of the new text.
type Hunk struct {
	Kind  Kind
	Line  int
	Count int
}

// Lines hashes each line of text. A trailing "\r" is ignored, so CRLF and LF
// copies of a file compare equal.
func Lines(text string) []uint64 {
	hashes := make([]uint64, 0, strings.Count(text, "\n")+1)
	for {
		line, rest, found := strings.Cut(text, "\n")
		line = strings.TrimSuffix(line, "\r")
		hashes = append(hashes, fnv64a(line))
		if !found {
			return hashes
		}
		text = rest
	}
}

// fnv64a is FNV-1a over a string, without the allocation of hash/fnv.
func fnv64a(text string) uint64 {
	hash := uint64(14695981039346656037)
	for i := 0; i < len(text); i++ {
		hash ^= uint64(text[i])
		hash *= 1099511628211
	}
	return hash
}

type op byte

const (
	equal op = iota
	insert
	remove
)

// Compare returns the hunks that turn old into next.
func Compare(old, next []uint64) []Hunk {
	prefix := 0
	for prefix < len(old) && prefix < len(next) && old[prefix] == next[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(next)-prefix &&
		old[len(old)-1-suffix] == next[len(next)-1-suffix] {
		suffix++
	}
	a, b := old[prefix:len(old)-suffix], next[prefix:len(next)-suffix]
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	ops, ok := myers(a, b)
	if !ok {
		return group(nil, prefix, len(a), len(b))
	}
	var hunks []Hunk
	line, removed, inserted := prefix, 0, 0
	flush := func() {
		if removed > 0 || inserted > 0 {
			hunks = group(hunks, line-inserted, removed, inserted)
		}
		removed, inserted = 0, 0
	}
	for _, o := range ops {
		switch o {
		case equal:
			flush()
			line++
		case insert:
			inserted++
			line++
		case remove:
			removed++
		}
	}
	flush()
	return hunks
}

// group appends the hunks for one run of edits at line. When a run inserts
// more lines than it removes, the extra lines show as added after the
// modified ones.
func group(hunks []Hunk, line, removed, inserted int) []Hunk {
	switch {
	case inserted == 0:
		return append(hunks, Hunk{Kind: Deleted, Line: line})
	case removed == 0:
		return append(hunks, Hunk{Kind: Added, Line: line, Count: inserted})
	case inserted > removed:
		return append(hunks, Hunk{Kind: Modified, Line: line, Count: removed},
			Hunk{Kind: Added, Line: line + removed, Count: inserted - removed})
	default:
		return append(hunks, Hunk{Kind: Modified, Line: line, Count: inserted})
	}
}

// myers returns the shortest edit script from a to b, or false when it needs
// more than maxEdits edits.
func myers(a, b []uint64) ([]op, bool) {
	n, m := len(a), len(b)
	limit := min(n+m, maxEdits)
	offset := limit + 1
	v := make([]int, 2*offset+1)
	// trace[d] holds v for k in [-d-1, d+1] as it was before step d.
	var trace [][]int
	for d := 0; d <= limit; d++ {
		trace = append(trace, append([]int(nil), v[offset-d-1:offset+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m), true
			}
		}
	}
	return nil, false
}

func backtrack(trace [][]int, x, y int) []op {
	var ops []op
	for d := len(trace) - 1; d >= 0; d-- {
		row := trace[d]
		at := func(k int) int { return row[k+d+1] }
		k := x - y
		var prevK int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := at(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			ops = append(ops, equal)
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				ops = append(ops, insert)
			} else {
				ops = append(ops, remove)
			}
		}
		x, y = prevX, prevY
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
