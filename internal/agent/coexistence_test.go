package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/edit"
)

func TestClaudeRestoresAliasesAndPreservesLaterChanges(t *testing.T) {
	home, _ := codexHome(t, routerAuth, routerConfig)
	a := claude(home)
	path := filepath.Join(home, ".claude", "settings.json")
	if err := edit.SetJSON(path,
		edit.KV{Path: "env.ANTHROPIC_BASE_URL", Value: "https://relay.example"},
		edit.KV{Path: "env.ANTHROPIC_AUTH_TOKEN", Value: "old-token"},
		edit.KV{Path: "env.ANTHROPIC_DEFAULT_HAIKU_MODEL", Value: "original-haiku"},
		edit.KV{Path: "env.CLAUDE_CODE_SUBAGENT_MODEL", Value: "original-subagent"},
	); err != nil {
		t.Fatal(err)
	}
	if err := a.Field("model").Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	// A tool changed the token and another tier without replacing the endpoint.
	if err := edit.SetJSON(path, edit.KV{Path: "env.ANTHROPIC_AUTH_TOKEN", Value: "new-token"}, edit.KV{Path: "env.ANTHROPIC_DEFAULT_OPUS_MODEL", Value: "new-opus"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"ANTHROPIC_BASE_URL": "https://relay.example", "ANTHROPIC_AUTH_TOKEN": "new-token",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "original-haiku", "CLAUDE_CODE_SUBAGENT_MODEL": "original-subagent",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "new-opus",
	} {
		if got, _ := edit.GetJSON(path, "env."+k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	before, _ := os.ReadFile(path)
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("repeated default changed restored configuration")
	}
}

func TestConfigDefaultPreservesAnotherOwner(t *testing.T) {
	for _, id := range []string{"claude", "codex"} {
		t.Run(id, func(t *testing.T) {
			home, _ := codexHome(t, routerAuth, routerConfig)
			a := codex(home)
			if id == "claude" {
				a = claude(home)
			}
			if err := a.Field("model").Set("fake/m1"); err != nil {
				t.Fatal(err)
			}
			if id == "codex" {
				if err := edit.SetTOMLTop(a.Path, edit.KV{Path: "openai_base_url", Value: "https://new.example/v1"}, edit.KV{Path: "model_provider", Value: "new-owner"}, edit.KV{Path: "model_catalog_json", Value: "/new/catalog.json"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := edit.SetJSON(a.Path, edit.KV{Path: "env.ANTHROPIC_BASE_URL", Value: "https://new.example"}, edit.KV{Path: "env.ANTHROPIC_AUTH_TOKEN", Value: "new-token"}); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := a.Field("model").Set(""); err != nil {
					t.Fatal(err)
				}
			}
			if id == "codex" {
				for k, v := range map[string]string{"openai_base_url": "https://new.example/v1", "model_provider": "new-owner", "model_catalog_json": "/new/catalog.json"} {
					if got := top(t, home, k); got != v {
						t.Errorf("%s overwritten: %q", k, got)
					}
				}
			} else if got, _ := edit.GetJSON(a.Path, "env.ANTHROPIC_AUTH_TOKEN"); got != "new-token" {
				t.Fatal("new relay overwritten")
			}
		})
	}
}

func TestConfigFailedRestoreCanBeRetried(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory write permissions")
	}
	for _, id := range []string{"claude", "codex"} {
		t.Run(id, func(t *testing.T) {
			home, _ := codexHome(t, routerAuth, routerConfig)
			a := codex(home)
			if id == "claude" {
				a = claude(home)
				if err := edit.SetJSON(a.Path, edit.KV{Path: "env.ANTHROPIC_BASE_URL", Value: "https://relay.example"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Field("model").Set("fake/m1"); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(a.Path)
			recovery, _ := os.ReadFile(stashPath())
			dir := filepath.Dir(a.Path)
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			if err := a.Field("model").Set(""); err == nil {
				t.Fatal("restore unexpectedly succeeded in read-only directory")
			}
			after, _ := os.ReadFile(a.Path)
			state, _ := os.ReadFile(stashPath())
			if string(before) != string(after) || string(recovery) != string(state) {
				t.Fatal("failed restore changed configuration or consumed recovery data")
			}
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := a.Field("model").Set(""); err != nil {
				t.Fatal(err)
			}
			if id == "codex" && top(t, home, "model_provider") != "custom" {
				t.Fatal("retry did not restore provider")
			}
			if id == "claude" {
				if got, _ := edit.GetJSON(a.Path, "env.ANTHROPIC_BASE_URL"); got != "https://relay.example" {
					t.Fatal("retry did not restore relay")
				}
			}
		})
	}
}

func TestCodexRestoreSurvivesManagedBlockRegeneration(t *testing.T) {
	home, read := codexHome(t, routerAuth, routerConfig)
	a := codex(home)
	if err := a.Field("model").Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	cfg := read()
	start, end := strings.Index(cfg, "# BEGIN codex-router-managed"), strings.Index(cfg, "# END codex-router-managed")
	if start < 0 || end < start {
		t.Fatal("managed markers lost")
	}
	cfg = cfg[:start] + cfg[end+len("# END codex-router-managed"):]
	if err := os.WriteFile(a.Path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if top(t, home, "model_provider") != "custom" {
		t.Fatal("regenerating the managed block swallowed model_provider")
	}
}

func TestTakeoverRequiresWritableRecoveryState(t *testing.T) {
	for _, id := range []string{"claude", "codex"} {
		t.Run(id, func(t *testing.T) {
			home, _ := codexHome(t, routerAuth, routerConfig)
			a := codex(home)
			if id == "claude" {
				a = claude(home)
				if err := edit.SetJSON(a.Path, edit.KV{Path: "theme", Value: "dark"}); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(a.Path)
			if err := os.MkdirAll(stashPath(), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := a.Field("model").Set("fake/m1"); err == nil {
				t.Fatal("took over without recovery state")
			}
			after, _ := os.ReadFile(a.Path)
			if string(before) != string(after) {
				t.Fatal("takeover altered configuration")
			}
		})
	}
}
