package agent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
)

// Claude Code reads its endpoint from the `env` block of settings.json.
// Pointing ANTHROPIC_BASE_URL at the gateway and naming a catalog model in
// ANTHROPIC_MODEL (and the aliases opus/sonnet/haiku resolve through) is
// all it takes to run it on any provider.

// env vars magpie sets while routing through the gateway.
var claudeEnv = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL",
}

// claudeTiers are the aliases Claude Code resolves (/model opus, a
// subagent's "model: haiku", …), each of which can have a model of its own.
// A tier that has none follows the main model.
var claudeTiers = []string{"opus", "sonnet", "haiku", "fable"}

func tierEnv(tier string) string { return "ANTHROPIC_DEFAULT_" + strings.ToUpper(tier) + "_MODEL" }

func claude(home string) *Agent {
	path := filepath.Join(home, ".claude", "settings.json")
	env := func(k string) string { v, _ := edit.GetJSON(path, "env."+k); return v }
	model := jsonGet(path, "model")
	routed := func() bool { return env("ANTHROPIC_BASE_URL") == gateway.URL() }

	// the value shown: the catalog ref while routed, else Claude's own model.
	get := func() string {
		if routed() {
			if m := env("ANTHROPIC_MODEL"); m != "" {
				return m
			}
		}
		return model()
	}
	var writeTiers func(target, main string, tiers map[string]string) error
	stashKeys := []string{"claude.model", "claude.base_url", "claude.auth_token"}
	for _, k := range claudeEnv {
		stashKeys = append(stashKeys, "claude.env."+k)
	}
	set := func(v string) error {
		owned := routed()
		previous, err := stashRead()
		if err != nil {
			return err
		}
		if isMagpie(v) && !owned {
			saved := map[string]string{
				"claude.model": model(), "claude.base_url": env("ANTHROPIC_BASE_URL"),
				"claude.auth_token": env("ANTHROPIC_AUTH_TOKEN"),
			}
			for _, k := range claudeEnv {
				saved["claude.env."+k] = env(k)
			}
			if err := stashUpdate(saved); err != nil {
				return err
			}
		}
		err = edit.Transaction(path, func(target string) error {
			if isMagpie(v) {
				tiers := map[string]string{}
				for _, t := range claudeTiers {
					tiers[t] = v
					if w := env(tierEnv(t)); owned && w != "" && w != env("ANTHROPIC_MODEL") && isMagpie(w) {
						tiers[t] = w
					}
				}
				return writeTiers(target, v, tiers)
			}
			// Once another tool has taken the endpoint, its configuration
			// wins. Repeated default must not delete that tool's relay.
			if owned {
				for _, k := range claudeEnv {
					current := env(k)
					want := previous["claude.env."+k]
					switch k {
					case "ANTHROPIC_BASE_URL":
						want = previous["claude.base_url"]
						if want == gateway.URL() {
							want = ""
						}
					case "ANTHROPIC_AUTH_TOKEN":
						if current != "" && current != gateway.Token {
							continue
						}
						want = previous["claude.auth_token"]
						if want == gateway.Token {
							want = ""
						}
					default:
						if current != "" && !isMagpie(current) {
							continue
						}
					}
					if want == "" {
						if err := edit.DelJSON(target, "env."+k); err != nil {
							return err
						}
					} else if err := edit.SetJSON(target, edit.KV{Path: "env." + k, Value: want}); err != nil {
						return err
					}
				}
			}
			if v == "" {
				return edit.DelJSON(target, "model")
			}
			return edit.SetJSON(target, edit.KV{Path: "model", Value: v})
		})
		if err != nil || isMagpie(v) {
			return err
		}
		return stashUpdate(nil, stashKeys...)
	}

	// writeTiers routes Claude Code through the gateway with main as its
	// model and each tier on the model given.
	writeTiers = func(target, main string, tiers map[string]string) error {
		kvs := []edit.KV{
			{Path: "env.ANTHROPIC_BASE_URL", Value: gateway.URL()},
			{Path: "env.ANTHROPIC_AUTH_TOKEN", Value: gateway.Token},
			{Path: "env.ANTHROPIC_MODEL", Value: main},
			{Path: "env.ANTHROPIC_SMALL_FAST_MODEL", Value: tiers["haiku"]},
			{Path: "model", Value: main},
		}
		same := true
		for _, t := range claudeTiers {
			kvs = append(kvs, edit.KV{Path: "env." + tierEnv(t), Value: tiers[t]})
			same = same && tiers[t] == main
		}
		if !same {
			// one model for every subagent would override the tiers they ask for
			if err := edit.DelJSON(target, "env.CLAUDE_CODE_SUBAGENT_MODEL"); err != nil {
				return err
			}
		} else {
			kvs = append(kvs, edit.KV{Path: "env.CLAUDE_CODE_SUBAGENT_MODEL", Value: main})
		}
		return edit.SetJSON(target, kvs...)
	}

	fields := []Field{{
		Key: "model", Label: "model",
		Get: get,
		Set: set,
		Options: func(map[string]string) []Option {
			var own []Option
			for _, m := range catalog.Provider("anthropic") {
				if strings.HasPrefix(m.ID, "claude") {
					own = append(own, Option{Value: m.ID, Note: m.Name, Icon: "claude-color"})
				}
			}
			name := "Claude Code"
			if u := env("ANTHROPIC_BASE_URL"); u != "" && !routed() {
				name += " · " + hostOf(u)
			}
			// Only the catalog's models; Claude Code's own short aliases are
			// not something any API lists, and a compiled-in copy would just
			// go stale.
			return append(group(name, own), viaMagpie("")...)
		},
	}}
	for _, tier := range claudeTiers {
		fields = append(fields, Field{
			Key: tier, Label: tier, Quiet: true,
			// empty while the tier follows the main model
			Get: func() string {
				if w := env(tierEnv(tier)); routed() && w != env("ANTHROPIC_MODEL") {
					return w
				}
				return ""
			},
			Set: func(v string) error {
				if !routed() {
					if v == "" {
						return nil
					}
					return fmt.Errorf("pick a model through magpie for Claude Code first; %s can then have its own", tier)
				}
				if v != "" && !isMagpie(v) {
					return fmt.Errorf("%s: %q is not a model magpie serves", tier, v)
				}
				main := env("ANTHROPIC_MODEL")
				tiers := map[string]string{}
				for _, t := range claudeTiers {
					tiers[t] = env(tierEnv(t))
					if tiers[t] == "" {
						tiers[t] = main
					}
				}
				tiers[tier] = v
				if v == "" {
					tiers[tier] = main
				}
				return edit.Transaction(path, func(target string) error { return writeTiers(target, main, tiers) })
			},
			Options: func(map[string]string) []Option {
				if !routed() {
					return nil
				}
				return viaMagpie("")
			},
		})
	}

	return &Agent{
		ID: "claude", Name: "Claude Code", Icon: "claudecode-color", Aliases: []string{"cc", "claude-code"},
		UA:  []string{"claude-cli", "claude-code"},
		Bin: "claude", Dir: filepath.Dir(path), Path: path,
		Fields: fields,
		Check: func() string {
			if !isMagpie(get()) {
				return ""
			}
			// an administrator's settings win over the user's
			if u, _ := edit.GetJSON(claudeManaged(), "env.ANTHROPIC_BASE_URL"); u != "" && u != gateway.URL() {
				return "Claude Code's managed settings (" + claudeManaged() + ") set ANTHROPIC_BASE_URL to " + u + ", which wins over magpie's"
			}
			return wiringOff("Claude Code", path, func(k string) (string, bool) { return edit.GetJSON(path, "env."+k) },
				"ANTHROPIC_BASE_URL", gateway.URL(), "ANTHROPIC_AUTH_TOKEN", gateway.Token)
		},
		// every prompt typed into Claude Code goes into history.jsonl
		LastUsed: func() time.Time {
			return lastJSONLTime(filepath.Join(filepath.Dir(path), "history.jsonl"), "timestamp", "display")
		},
	}
}

// claudeManaged is where an administrator's Claude Code settings live; a var
// so tests can point it elsewhere.
var claudeManaged = func() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "windows":
		return `C:\ProgramData\ClaudeCode\managed-settings.json`
	}
	return "/etc/claude-code/managed-settings.json"
}
