package document

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkOpenFiveMegabytes(b *testing.B) {
	path := filepath.Join(b.TempDir(), "large.txt")
	line := []byte("The quick brown fox jumps over the lazy dog.\n")
	data := bytes.Repeat(line, 5*1024*1024/len(line)+1)
	data = data[:5*1024*1024]
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatalf("write fixture: %v", err)
	}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := Open(path); err != nil {
			b.Fatalf("open fixture: %v", err)
		}
	}
}
