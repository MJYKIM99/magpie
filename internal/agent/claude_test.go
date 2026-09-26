package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

func TestClaudeTiers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if err := provider.Save(provider.Provider{ID: "deepseek", Name: "DeepSeek", Chat: "https://api.deepseek.com/v1", Key: "k", Models: []string{"pro", "flash"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme": "dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a := claude(home)
	env := func(k string) string { v, _ := edit.GetJSON(path, "env."+k); return v }

	if err := a.Field("haiku").Set("deepseek/flash"); err == nil {
		t.Fatal("a tier before Claude Code is routed should fail")
	}
	if err := a.Field("model").Set("deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	if env("ANTHROPIC_DEFAULT_HAIKU_MODEL") != "deepseek/pro" || env("CLAUDE_CODE_SUBAGENT_MODEL") != "deepseek/pro" || a.Field("haiku").Get() != "" {
		t.Fatalf("tiers should follow the model: %v", a.Values())
	}
	if err := a.Field("haiku").Set("deepseek/flash"); err != nil {
		t.Fatal(err)
	}
	if env("ANTHROPIC_DEFAULT_HAIKU_MODEL") != "deepseek/flash" || env("ANTHROPIC_SMALL_FAST_MODEL") != "deepseek/flash" || env("CLAUDE_CODE_SUBAGENT_MODEL") != "" {
		t.Fatalf("haiku: %v", a.Values())
	}
	if env("ANTHROPIC_DEFAULT_OPUS_MODEL") != "deepseek/pro" || a.Field("haiku").Get() != "deepseek/flash" {
		t.Fatalf("others: %v", a.Values())
	}
	// a new main model carries the tiers that followed it, not haiku
	if err := a.Field("model").Set("deepseek/flash"); err != nil {
		t.Fatal(err)
	}
	if err := a.Field("model").Set("deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	if env("ANTHROPIC_DEFAULT_SONNET_MODEL") != "deepseek/pro" || env("ANTHROPIC_DEFAULT_FABLE_MODEL") != "deepseek/pro" {
		t.Fatalf("sonnet/fable should follow: %v", a.Values())
	}
	if err := a.Field("haiku").Set(""); err != nil {
		t.Fatal(err)
	}
	if env("ANTHROPIC_DEFAULT_HAIKU_MODEL") != "deepseek/pro" || env("CLAUDE_CODE_SUBAGENT_MODEL") != "deepseek/pro" {
		t.Fatalf("haiku back to the model: %v", a.Values())
	}
	// back to Claude Code as installed
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	if env("ANTHROPIC_DEFAULT_FABLE_MODEL") != "" || env("ANTHROPIC_BASE_URL") != "" {
		t.Fatal("reset left env behind")
	}
	if v, _ := edit.GetJSON(path, "theme"); v != "dark" {
		t.Fatal("theme lost")
	}
}

// Coexistence: a relay another tool put Claude Code on comes back when
// magpie steps out, whichever way out is taken, and the settings that are
// nobody's business but the user's — the proxy another tool installed
// above all — are never touched.
func TestClaudeHandsBackAnotherToolsRelay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if err := provider.Save(provider.Provider{ID: "deepseek", Name: "DeepSeek", Chat: "https://api.deepseek.com/v1", Key: "k", Models: []string{"pro"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	relay := `{"theme":"dark","env":{"ANTHROPIC_BASE_URL":"https://relay.example.com",` +
		`"ANTHROPIC_AUTH_TOKEN":"relay-token","HTTPS_PROXY":"http://127.0.0.1:17899","NO_PROXY":"127.0.0.1"}}`
	if err := os.WriteFile(path, []byte(relay), 0o644); err != nil {
		t.Fatal(err)
	}
	a := claude(home)
	env := func(k string) string { v, _ := edit.GetJSON(path, "env."+k); return v }
	relayIsBack := func(step string) {
		t.Helper()
		if env("ANTHROPIC_BASE_URL") != "https://relay.example.com" || env("ANTHROPIC_AUTH_TOKEN") != "relay-token" {
			t.Fatalf("%s: the relay was not handed back (%q, %q)", step, env("ANTHROPIC_BASE_URL"), env("ANTHROPIC_AUTH_TOKEN"))
		}
		if env("HTTPS_PROXY") != "http://127.0.0.1:17899" || env("NO_PROXY") != "127.0.0.1" {
			t.Fatalf("%s: the user's proxy settings moved: %v", step, a.Values())
		}
	}

	if err := a.Field("model").Set("deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	if env("ANTHROPIC_BASE_URL") != gateway.URL() || env("ANTHROPIC_AUTH_TOKEN") != gateway.Token {
		t.Fatalf("not routed through magpie: %v", a.Values())
	}
	if env("HTTPS_PROXY") != "http://127.0.0.1:17899" {
		t.Fatal("routing dropped the user's proxy settings")
	}

	// one of Claude Code's own models
	if err := a.Field("model").Set("claude-sonnet-4-5"); err != nil {
		t.Fatal(err)
	}
	relayIsBack("own model")

	// and the agent's own default
	if err := a.Field("model").Set("deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	relayIsBack("reset")
	if v, _ := edit.GetJSON(path, "model"); v != "" {
		t.Fatalf("reset left a model behind: %q", v)
	}
}
