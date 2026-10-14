// Package editorconfig reads the core EditorConfig properties used by E.
package editorconfig

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Properties are the supported settings resolved for one file.
type Properties struct {
	IndentStyle            string
	IndentSize             int
	TabWidth               int
	EndOfLine              string
	Charset                string
	TrimTrailingWhitespace *bool
	InsertFinalNewline     *bool
	MaxLineLength          int
}

// Resolve walks from filename's directory to the filesystem root and applies
// matching .editorconfig sections from outermost to innermost.
func Resolve(filename string) (Properties, error) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return Properties{}, fmt.Errorf("resolve filename: %w", err)
	}
	var files []string
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, ".editorconfig")
		if _, err := os.Stat(candidate); err == nil {
			files = append(files, candidate)
			root, err := hasRoot(candidate)
			if err != nil {
				return Properties{}, err
			}
			if root {
				break
			}
		} else if !os.IsNotExist(err) {
			return Properties{}, fmt.Errorf("inspect %s: %w", candidate, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	var state resolveState
	for i := len(files) - 1; i >= 0; i-- {
		if err := applyFile(files[i], abs, &state); err != nil {
			return Properties{}, err
		}
	}
	out := state.props
	if state.indentSizeTab {
		out.IndentSize = out.TabWidth
	}
	if out.IndentSize == 0 {
		out.IndentSize = out.TabWidth
	}
	if out.TabWidth == 0 {
		out.TabWidth = out.IndentSize
	}
	return out, nil
}

// resolveState collects properties across sections and files. indent_size =
// tab is kept as a flag so it uses the final tab_width, not the one seen so far.
type resolveState struct {
	props         Properties
	indentSizeTab bool
}

func hasRoot(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Debug("close EditorConfig", "path", path, "err", err)
		}
	}()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			break
		}
		key, value, ok := splitProperty(line)
		if ok && strings.EqualFold(key, "root") && strings.EqualFold(value, "true") {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return false, nil
}

func applyFile(path, filename string, out *resolveState) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Debug("close EditorConfig", "path", path, "err", err)
		}
	}()
	rel, err := filepath.Rel(filepath.Dir(path), filename)
	if err != nil {
		return fmt.Errorf("resolve path relative to %s: %w", path, err)
	}
	rel = filepath.ToSlash(rel)
	active := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			active = matches(strings.TrimSpace(line[1:len(line)-1]), rel)
			continue
		}
		if !active {
			continue
		}
		key, value, ok := splitProperty(line)
		if ok {
			applyProperty(out, strings.ToLower(key), strings.ToLower(value))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func splitProperty(line string) (string, string, bool) {
	idx := strings.IndexAny(line, "=:")
	if idx < 1 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

func applyProperty(state *resolveState, key, value string) {
	out := &state.props
	if value == "unset" {
		if key == "indent_size" {
			state.indentSizeTab = false
		}
		clearProperty(out, key)
		return
	}
	parsePositive := func() int {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return 0
		}
		return n
	}
	parseBool := func() *bool {
		b, err := strconv.ParseBool(value)
		if err != nil {
			return nil
		}
		return &b
	}
	switch key {
	case "indent_style":
		if value == "space" || value == "tab" {
			out.IndentStyle = value
		}
	case "indent_size":
		if value == "tab" {
			state.indentSizeTab = true
		} else if n := parsePositive(); n > 0 {
			state.indentSizeTab = false
			out.IndentSize = n
		}
	case "tab_width":
		out.TabWidth = parsePositive()
	case "end_of_line":
		if value == "lf" || value == "crlf" || value == "cr" {
			out.EndOfLine = value
		}
	case "charset":
		out.Charset = value
	case "trim_trailing_whitespace":
		out.TrimTrailingWhitespace = parseBool()
	case "insert_final_newline":
		out.InsertFinalNewline = parseBool()
	case "max_line_length":
		out.MaxLineLength = parsePositive()
	}
}

func clearProperty(out *Properties, key string) {
	switch key {
	case "indent_style":
		out.IndentStyle = ""
	case "indent_size":
		out.IndentSize = 0
	case "tab_width":
		out.TabWidth = 0
	case "end_of_line":
		out.EndOfLine = ""
	case "charset":
		out.Charset = ""
	case "trim_trailing_whitespace":
		out.TrimTrailingWhitespace = nil
	case "insert_final_newline":
		out.InsertFinalNewline = nil
	case "max_line_length":
		out.MaxLineLength = 0
	}
}
