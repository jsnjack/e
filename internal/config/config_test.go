package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultModelOrder(t *testing.T) {
	cfg := Default()
	if cfg.AI.Enabled {
		t.Fatal("automatic AI suggestions must require explicit opt-in")
	}
	if cfg.AI.Consented {
		t.Fatal("AI data sharing must require explicit consent")
	}
	want := []string{"Gemma-4-E4B-it-GGUF"}
	if len(cfg.AI.Chain) != len(want) {
		t.Fatalf("model count = %d, want %d", len(cfg.AI.Chain), len(want))
	}
	for index, name := range want {
		if cfg.AI.Chain[index].Name != name {
			t.Fatalf("model %d = %q, want %q", index, cfg.AI.Chain[index].Name, name)
		}
	}
}

func TestConfigRoundTripNormalizesValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e", "config.toml")
	cfg := Config{
		AI:     AI{Enabled: true, Chain: []Model{{Name: "local", Endpoint: "http://localhost:8000/v1"}}},
		Editor: Editor{TabWidth: 99},
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Editor.TabWidth != 4 || loaded.AI.DebounceMS != defaultDebounceMS {
		t.Fatalf("normalized config = %+v", loaded)
	}
	if len(loaded.AI.Chain) != 1 || loaded.AI.Chain[0].Provider != "openai" || loaded.AI.Chain[0].Protocol != "chat" {
		t.Fatalf("normalized model = %+v", loaded.AI.Chain)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o", info.Mode().Perm())
	}
}

func TestFirstRunImportsMailboxModelOrder(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	mailbox := filepath.Join(configHome, "mailbox", "config.toml")
	if err := os.MkdirAll(filepath.Dir(mailbox), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[ai]
provider = "openai"
endpoint = "https://models.example/v1"

[[ai.chain]]
model = "qwen"

[[ai.chain]]
model = "gemma"
endpoint = "http://localhost:13305/v1"
allow_insecure_http = true
`
	if err := os.WriteFile(mailbox, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(filepath.Join(configHome, "e", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AI.Chain) != 2 || cfg.AI.Chain[0].Name != "qwen" || cfg.AI.Chain[1].Name != "gemma" {
		t.Fatalf("imported chain = %+v", cfg.AI.Chain)
	}
	if cfg.AI.Chain[0].Endpoint != "https://models.example/v1" || cfg.AI.Chain[1].Endpoint != "http://localhost:13305/v1" {
		t.Fatalf("imported endpoints = %+v", cfg.AI.Chain)
	}
}
