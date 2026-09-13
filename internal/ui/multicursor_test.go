package ui

import "testing"

func TestMultiCursorBoundsPreserveDirection(t *testing.T) {
	tests := []struct {
		cursor     multiCursor
		start, end int
	}{
		{multiCursor{anchor: 2, head: 8}, 2, 8},
		{multiCursor{anchor: 8, head: 2}, 2, 8},
		{multiCursor{anchor: 4, head: 4}, 4, 4},
	}
	for _, test := range tests {
		start, end := test.cursor.bounds()
		if start != test.start || end != test.end {
			t.Fatalf("bounds(%+v) = (%d, %d), want (%d, %d)", test.cursor, start, end, test.start, test.end)
		}
	}
}
