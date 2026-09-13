package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	defaultDebounceMS = 120
	defaultPrefixSize = 12 * 1024
	defaultSuffixSize = 4 * 1024
)

// Model configures one entry in the ordered autocomplete chain.
type Model struct {
	Name              string `toml:"name"`
	Provider          string `toml:"provider"`
	Endpoint          string `toml:"endpoint"`
	Protocol          string `toml:"protocol,omitempty"`
	AllowInsecureHTTP bool   `toml:"allow_insecure_http,omitempty"`
}

// AI configures automatic inline completion.
type AI struct {
	Enabled    bool    `toml:"enabled"`
	DebounceMS int     `toml:"debounce_ms"`
	PrefixSize int     `toml:"prefix_bytes"`
	SuffixSize int     `toml:"suffix_bytes"`
	Chain      []Model `toml:"chain"`
}

// LSPServer maps a language id to an external language-server command.
type LSPServer struct {
	Language string   `toml:"language"`
	Command  string   `toml:"command"`
	Args     []string `toml:"args,omitempty"`
}

// Editor contains presentation and editing preferences.
type Editor struct {
	LineNumbers  bool `toml:"line_numbers"`
	WordWrap     bool `toml:"word_wrap"`
	SpellCheck   bool `toml:"spell_check"`
	TabWidth     int  `toml:"tab_width"`
	InsertSpaces bool `toml:"insert_spaces"`
}

// Config is E's complete configuration file.
type Config struct {
	AI     AI          `toml:"ai"`
	Editor Editor      `toml:"editor"`
	LSP    []LSPServer `toml:"lsp"`
}

// Default returns the first-run settings approved for E.
func Default() Config {
	return Config{
		AI: AI{
			Enabled: true, DebounceMS: defaultDebounceMS,
			PrefixSize: defaultPrefixSize, SuffixSize: defaultSuffixSize,
			Chain: []Model{
				{Name: "local-qwen", Provider: "openai", Endpoint: "https://models.example.com/v1", Protocol: "chat"},
				{Name: "Gemma-4-E4B-it-GGUF", Provider: "openai", Endpoint: "http://localhost:13305/v1", Protocol: "chat"},
			},
		},
		Editor: Editor{LineNumbers: true, SpellCheck: true, TabWidth: 4, InsertSpaces: true},
	}
}

// Load reads path, importing Mailbox's AI chain when this is E's first run.
func Load(path string) (Config, error) {
	cfg := Default()
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
		normalize(&cfg)
		return cfg, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("inspect config %s: %w", path, err)
	}
	if imported, ok := importMailboxAI(); ok {
		cfg.AI.Chain = imported
	}
	if err := Save(path, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save atomically persists cfg.
func Save(path string, cfg Config) error {
	normalize(&cfg)
	var out bytes.Buffer
	if err := toml.NewEncoder(&out).Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := WriteFileAtomic(path, out.Bytes(), 0o600); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func normalize(cfg *Config) {
	if cfg.AI.DebounceMS <= 0 {
		cfg.AI.DebounceMS = defaultDebounceMS
	}
	if cfg.AI.PrefixSize <= 0 {
		cfg.AI.PrefixSize = defaultPrefixSize
	}
	if cfg.AI.SuffixSize <= 0 {
		cfg.AI.SuffixSize = defaultSuffixSize
	}
	if cfg.Editor.TabWidth < 1 || cfg.Editor.TabWidth > 16 {
		cfg.Editor.TabWidth = 4
	}
	for i := range cfg.AI.Chain {
		if cfg.AI.Chain[i].Provider == "" {
			cfg.AI.Chain[i].Provider = "openai"
		}
		if cfg.AI.Chain[i].Protocol == "" {
			cfg.AI.Chain[i].Protocol = "chat"
		}
	}
}

func importMailboxAI() ([]Model, bool) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, false
	}
	path := filepath.Join(configDir, "mailbox", "config.toml")
	var source struct {
		AI struct {
			Provider string `toml:"provider"`
			Endpoint string `toml:"endpoint"`
			Model    string `toml:"model"`
			Chain    []struct {
				Model             string `toml:"model"`
				Provider          string `toml:"provider"`
				Endpoint          string `toml:"endpoint"`
				AllowInsecureHTTP bool   `toml:"allow_insecure_http"`
			} `toml:"chain"`
		} `toml:"ai"`
	}
	if _, err := toml.DecodeFile(path, &source); err != nil {
		return nil, false
	}
	var out []Model
	for _, entry := range source.AI.Chain {
		provider := entry.Provider
		if provider == "" {
			provider = source.AI.Provider
		}
		endpoint := entry.Endpoint
		if endpoint == "" {
			endpoint = source.AI.Endpoint
		}
		if entry.Model != "" && provider != "" && endpoint != "" {
			out = append(out, Model{Name: entry.Model, Provider: provider, Endpoint: endpoint, Protocol: "chat", AllowInsecureHTTP: entry.AllowInsecureHTTP})
		}
	}
	if len(out) == 0 && source.AI.Model != "" && source.AI.Endpoint != "" {
		out = append(out, Model{Name: source.AI.Model, Provider: source.AI.Provider, Endpoint: source.AI.Endpoint, Protocol: "chat"})
	}
	return out, len(out) > 0
}
