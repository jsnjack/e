package lsp

import (
	"os/exec"
	"strings"

	"github.com/jsnjack/e/internal/config"
)

// CommandFor finds an explicitly configured or conventional installed server.
func CommandFor(language string, configured []config.LSPServer) (string, []string, bool) {
	for _, server := range configured {
		if server.Language == language && server.Command != "" {
			return server.Command, append([]string(nil), server.Args...), true
		}
	}
	defaults := map[string][]string{
		"go":         {"gopls"},
		"python":     {"pylsp"},
		"c":          {"clangd"},
		"cpp":        {"clangd"},
		"rust":       {"rust-analyzer"},
		"javascript": {"typescript-language-server", "--stdio"},
		"typescript": {"typescript-language-server", "--stdio"},
		"json":       {"vscode-json-language-server", "--stdio"},
	}
	command := defaults[strings.ToLower(language)]
	if len(command) == 0 {
		return "", nil, false
	}
	if _, err := exec.LookPath(command[0]); err != nil {
		return "", nil, false
	}
	return command[0], command[1:], true
}
