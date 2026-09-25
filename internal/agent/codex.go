package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/codexcat"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// Codex talks the OpenAI Responses API. Signed in (ChatGPT or an API key),
// it is routed through magpie with `openai_base_url` alone: Codex keeps its
// built-in OpenAI provider and sign-in, so its own models, its threads
// (listed per provider) and the Codex app's model picker stay as they are,
// and magpie's gateway passes its own models through to OpenAI while it
// answers the rest — the model list included, so magpie's models join
// OpenAI's in /model. Not signed in, the built-in provider can't run, and
// magpie is a provider of its own: a [model_providers.magpie] table,
// `model_provider = "magpie"`, and a model catalog file for /model. So it
// is too for a ChatGPT account that has used its allowance up, which the
// Codex app won't send anything for, whoever serves the model.

func codex(home string) *Agent {
	dir := filepath.Join(home, ".codex")
	path := filepath.Join(dir, "config.toml")
	catalogPath := filepath.Join(dir, "magpie-models.json")
	get := func(k string) string { v, _ := edit.GetTOMLTop(path, k); return v }
	asProvider := func() bool { return get("model_provider") == magpieID }
	viaBase := func() bool { return isCodexGateway(get("openai_base_url")) }
	routed := func() bool { return asProvider() || viaBase() }
	models := func() []catalog.Model {
		switch {
		case asProvider():
			return magpieModels()
		case viaBase():
			return append(catalog.Codex(), magpieModels()...)
		}
		return catalog.Codex()
	}
	// keep the effort valid for the model; a fresh model gets its default.
	settle := func() error {
		ms := models()
		model, effort := get("model"), get("model_reasoning_effort")
		if e := catalog.Efforts(ms, model); len(e) > 0 && !contains(e, effort) {
			return edit.SetTOMLTop(path, edit.KV{Path: "model_reasoning_effort", Value: codexcat.DefaultEffort(e)})
		}
		return nil
	}
	// dropProvider takes magpie out as a provider of Codex's.
	dropProvider := func() error {
		if !asProvider() {
			return nil
		}
		if err := edit.DelTOMLTop(path, "model_provider", "model_catalog_json"); err != nil {
			return err
		}
		if err := edit.DelTOMLTable(path, "model_providers."+magpieID); err != nil {
			return err
		}
		os.Remove(catalogPath)
		return nil
	}
	dropBase := func() error {
		if !viaBase() {
			return nil
		}
		return edit.DelTOMLTop(path, "openai_base_url")
	}
	// restore hands back what magpie found before it took Codex over: the
	// endpoint of another local gateway (see foreignGateway), the provider
	// and the catalog that were there. Values of magpie's own are dropped
	// rather than put back, and each stash entry is taken exactly once, so a
	// stale value cannot be restored over one another program has since
	// written.
	//
	// native says the user picked one of Codex's own models, so the effort
	// that model had comes back with it. The model itself never does: the
	// caller sets the one that was picked, and asking for the agent's own
	// default leaves none — which is what its `default` means.
	//
	// Keys go back at the end of the root section, which can be inside
	// another router's own marked block when that is where it keeps them.
	// That is harmless for the two keys it manages itself — it rewrites them
	// with the same values it gave magpie — and model_provider, the one it
	// does not manage, costs only itself if that router later regenerates
	// its block: Codex still reaches it through the base URL.
	restore := func(native bool) error {
		var back []edit.KV
		add := func(key, value string) {
			if value != "" {
				back = append(back, edit.KV{Path: key, Value: value})
			}
		}
		if b := unstash("codex.base_url"); b != "" && !isCodexGateway(b) {
			add("openai_base_url", b)
		}
		if p := unstash("codex.provider"); p != "" && p != magpieID {
			add("model_provider", p)
		}
		if c := unstash("codex.catalog"); c != "" && c != catalogPath {
			add("model_catalog_json", c)
		}
		if native {
			if e := unstash("codex.effort"); e != "" {
				add("model_reasoning_effort", e)
			}
		}
		unstash("codex.model")
		if len(back) == 0 {
			return nil
		}
		return edit.SetTOMLTop(path, back...)
	}
	// the model spawned subagents start on, when not the parent's; one of
	// magpie's goes when magpie steps out, as Codex could no longer find it
	subagent := func() string { return edit.GetTOMLTable(path, "agents")["default_subagent_model"] }
	dropSubagent := func() error {
		if !isMagpie(subagent()) {
			return nil
		}
		return edit.DelTOMLKey(path, "agents", "default_subagent_model")
	}
	modelOptions := func(withMagpie bool) []Option {
		var own []Option
		if p := get("model_provider"); p != "" && p != magpieID {
			own = group(p, options(catalog.Codex(), ""))
		} else {
			own = group("OpenAI", options(ownCodex(), ""))
		}
		if !withMagpie {
			return own
		}
		return append(own, viaMagpieFor("codex", "")...)
	}
	set := func(v string) error {
		if v == "" {
			if err := dropSubagent(); err != nil {
				return err
			}
			// magpie's own wiring comes out — its endpoint, its provider
			// table, its catalog file — and Codex is left as installed
			// otherwise
			if err := dropBase(); err != nil {
				return err
			}
			if err := edit.DelTOMLTop(path, "model", "model_provider", "model_catalog_json"); err != nil {
				return err
			}
			if err := edit.DelTOMLTable(path, "model_providers."+magpieID); err != nil {
				return err
			}
			os.Remove(catalogPath)
			// what another program had there — a router's endpoint above all
			// — goes back rather than staying deleted
			if err := restore(false); err != nil {
				return err
			}
			forget("codex.model", "codex.effort", "codex.provider", "codex.catalog", "codex.base_url")
			return nil
		}
		if isMagpie(v) {
			if !routed() {
				stash(map[string]string{"codex.model": get("model"), "codex.effort": get("model_reasoning_effort"),
					"codex.provider": get("model_provider"), "codex.catalog": get("model_catalog_json"),
					"codex.base_url": get("openai_base_url")})
			}
			// a ChatGPT account out of allowance keeps the Codex app from
			// sending at all, a magpie model's request too; as a provider
			// of Codex's own, magpie is past that
			if codexSignedIn(dir) && !codexUsedUp() {
				if err := dropProvider(); err != nil {
					return err
				}
				// the base URL is the built-in provider's, and a catalog
				// file would stand in for the list magpie hands out
				if err := edit.DelTOMLTop(path, "model_provider", "model_catalog_json"); err != nil {
					return err
				}
				if err := edit.SetTOMLTop(path,
					edit.KV{Path: "openai_base_url", Value: codexGatewayURL()},
					edit.KV{Path: "model", Value: v},
				); err != nil {
					return err
				}
				return settle()
			}
			if err := dropBase(); err != nil {
				return err
			}
			if err := edit.SetTOMLTable(path, "model_providers."+magpieID,
				edit.KV{Path: "name", Value: "magpie"},
				edit.KV{Path: "base_url", Value: gatewayV1()},
				edit.KV{Path: "wire_api", Value: "responses"},
				edit.KV{Path: "experimental_bearer_token", Value: gateway.Token},
			); err != nil {
				return err
			}
			if err := edit.WriteAtomic(catalogPath, codexcat.Catalog(magpieModels())); err != nil {
				return err
			}
			if err := edit.SetTOMLTop(path,
				edit.KV{Path: "model_provider", Value: magpieID},
				edit.KV{Path: "model_catalog_json", Value: catalogPath},
				edit.KV{Path: "model", Value: v},
			); err != nil {
				return err
			}
			return settle()
		}
		if routed() {
			if err := dropBase(); err != nil {
				return err
			}
			if err := dropProvider(); err != nil {
				return err
			}
			if err := dropSubagent(); err != nil {
				return err
			}
			if err := restore(true); err != nil {
				return err
			}
		}
		if err := edit.SetTOMLTop(path, edit.KV{Path: "model", Value: v}); err != nil {
			return err
		}
		return settle()
	}

	return &Agent{
		ID: "codex", Name: "Codex", Icon: "codex-color", Bin: "codex", Dir: dir, Path: path,
		UA: []string{"codex"},
		Sync: func() error {
			switch {
			case asProvider() && get("model_catalog_json") == catalogPath:
				b := codexcat.Catalog(magpieModels())
				if cur, _ := edit.Read(catalogPath); string(cur) != string(b) {
					if err := edit.WriteAtomic(catalogPath, b); err != nil {
						return err
					}
				}
			case viaBase():
				if err := codexStaleCache(filepath.Join(dir, "models_cache.json"), codexcat.Tag(provider.CodexListed())); err != nil {
					return err
				}
			default:
				return nil
			}
			// a model that now has levels it had none of before (models.dev
			// synced, a vendor's list fetched) keeps an effort it takes
			if isMagpie(get("model")) {
				return settle()
			}
			return nil
		},
		Check: func() string {
			if !isMagpie(get("model")) {
				return ""
			}
			// a profile's settings win over the top level's, magpie's included
			if p := get("profile"); p != "" {
				t := edit.GetTOMLTable(path, "profiles."+p)
				for _, k := range []string{"model", "model_provider", "openai_base_url", "model_catalog_json"} {
					if v, ok := t[k]; ok && v != get(k) {
						return "Codex's profile " + p + " sets its own " + k + " (" + v + "), which Codex takes over magpie's"
					}
				}
			}
			switch {
			case asProvider():
				t := edit.GetTOMLTable(path, "model_providers."+magpieID)
				if t["base_url"] != gatewayV1() || t["experimental_bearer_token"] != gateway.Token || t["wire_api"] != "responses" {
					return "Codex's [model_providers.magpie] no longer points at magpie's gateway (" + gatewayV1() + ")"
				}
				if c := get("model_catalog_json"); c != catalogPath {
					return "Codex's model_catalog_json is no longer magpie's list"
				}
				if _, err := os.Stat(catalogPath); err != nil {
					return "magpie's model list for Codex (" + catalogPath + ") is gone"
				}
			case viaBase():
				if u := get("openai_base_url"); strings.TrimSuffix(u, "/") != codexGatewayURL() {
					return "Codex's openai_base_url is " + u + ", not magpie's gateway at " + codexGatewayURL()
				}
			default:
				return "Codex's config no longer sends its model through magpie (no openai_base_url or model_provider of magpie's), so Codex asks OpenAI for a model OpenAI doesn't have"
			}
			return ""
		},
		// every prompt typed into Codex goes into history.jsonl
		LastUsed: func() time.Time { return lastJSONLTime(filepath.Join(dir, "history.jsonl"), "ts", "text") },
		// the Codex app writes no history.jsonl, but it and the TUI log each
		// model request they open
		Reached: func(since time.Time) (time.Time, string, bool) { return codexReached(dir, since) },
		// the app-server behind the Codex app (and every codex TUI) builds
		// its model list once, at start-up.
		Notice: func() string {
			var out []string
			if Running(`(^|/)codex( |$)`) {
				out = append(out, "Codex builds its model list at start-up — restart the Codex app (and open codex sessions) to see this.")
			}
			// magpie took Codex's endpoint over from another program on this
			// machine: while one of magpie's models is selected that program
			// is out of the loop, and one of Codex's own hands it back
			if b := stashLoad()["codex.base_url"]; routed() && foreignGateway(b) {
				out = append(out, "magpie took Codex's endpoint over from another local gateway at "+b+"; picking one of Codex's own models hands it back.")
			}
			return strings.Join(out, " ")
		},
		Fields: []Field{
			{
				Key: "model", Label: "model",
				Get:     func() string { return get("model") },
				Set:     set,
				Options: func(map[string]string) []Option { return modelOptions(true) },
			},
			{
				Key: "effort", Label: "effort",
				// unset, Codex takes the model's default, as the catalog
				// magpie wrote says it — shown as such rather than as none
				Get: func() string {
					if e := get("model_reasoning_effort"); e != "" {
						return e
					}
					if m := get("model"); isMagpie(m) {
						if e := catalog.Efforts(models(), m); len(e) > 0 {
							return codexcat.DefaultEffort(e)
						}
					}
					return ""
				},
				Set: func(v string) error {
					if v == "" {
						return edit.DelTOMLTop(path, "model_reasoning_effort")
					}
					return edit.SetTOMLTop(path, edit.KV{Path: "model_reasoning_effort", Value: v})
				},
				Options: func(cur map[string]string) []Option {
					if e := catalog.Efforts(models(), cur["model"]); len(e) > 0 {
						return static(e...)
					}
					return static("low", "medium", "high", "xhigh")
				},
			},
			{
				// Codex lists only the first few models in the spawn_agent
				// tool it gives the model, its own ahead of magpie's, so a
				// subagent is put on one of magpie's here, where it can't be
				// by the model unless asked by name
				Key: "subagent", Label: "subagents", Quiet: true,
				Get: subagent,
				Set: func(v string) error {
					if v == "" {
						return edit.DelTOMLKey(path, "agents", "default_subagent_model")
					}
					if isMagpie(v) && !routed() {
						return fmt.Errorf("pick a model through magpie for Codex first; its subagents can then have one of their own")
					}
					return edit.SetTOMLKey(path, "agents", "default_subagent_model", v)
				},
				Options: func(map[string]string) []Option { return modelOptions(routed()) },
			},
		},
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ownCodex is Codex's own models, narrowed to the ones ticked on its ChatGPT
// subscription in magpie when any are.
func ownCodex() []catalog.Model {
	ms := catalog.Codex()
	p, err := provider.Find("codex")
	if err != nil || len(p.Models) == 0 {
		return ms
	}
	var out []catalog.Model
	for _, m := range ms {
		if slices.Contains(p.Models, m.ID) {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return ms
	}
	return out
}

// codexGatewayURL is where Codex's built-in OpenAI provider is pointed to
// reach magpie.
func codexGatewayURL() string { return gateway.URL() + gateway.CodexPath }

// isCodexGateway reports whether an openai_base_url is magpie's, on
// whichever port it listened on then.
func isCodexGateway(u string) bool {
	return strings.HasPrefix(u, "http://127.0.0.1:") && strings.HasSuffix(strings.TrimSuffix(u, "/"), gateway.CodexPath)
}

// foreignGateway reports whether a base URL is another program's endpoint
// on this machine rather than a vendor's or magpie's — another router's own
// loopback port, typically. magpie takes that endpoint over while one of
// its models is selected, which puts the other program out of the loop, so
// it is worth saying so rather than shadowing it silently.
func foreignGateway(u string) bool {
	if u == "" || isCodexGateway(u) {
		return false
	}
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	switch p.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return false
	}
	// magpie's own gateway is not another program's, whichever port it was
	// moved to and however the URL spells its host
	_, ours, err := net.SplitHostPort(gateway.Addr())
	return err != nil || p.Port() != ours
}

// codexStaleCache ages Codex's cached model list if it isn't the one magpie
// would hand out now (its ETag carries the list's tag), so the next Codex to
// start asks the gateway again rather than showing the old one until the
// cache ages on its own; the rest of the cache stays as Codex wrote it.
func codexStaleCache(path, tag string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c map[string]json.RawMessage
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	var etag string
	json.Unmarshal(c["etag"], &etag)
	if codexcat.Tagged(etag, tag) {
		return nil
	}
	old := time.Unix(0, 0).UTC().Format(time.RFC3339)
	var at string
	if json.Unmarshal(c["fetched_at"], &at) == nil && at == old {
		return nil
	}
	c["fetched_at"], _ = json.Marshal(old)
	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return edit.WriteAtomic(path, out)
}

// codexUsedUp reports whether the ChatGPT account Codex is signed in to
// has used up its allowance. A var so tests can say.
var codexUsedUp = func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return provider.CodexUsedUp(ctx)
}

// codexSignedIn reports whether Codex has a sign-in of its own, a ChatGPT
// account or an API key, which its built-in OpenAI provider needs.
func codexSignedIn(dir string) bool {
	var a struct {
		Key    string `json:"OPENAI_API_KEY"`
		Tokens struct {
			Access string `json:"access_token"`
		} `json:"tokens"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil || json.Unmarshal(b, &a) != nil {
		return false
	}
	return a.Key != "" || a.Tokens.Access != ""
}

// codexReached reads Codex's newest model request since a time — the
// newest day of it at most — from the log database the Codex app and the
// TUI share: the address it opened, and whether that was refused (nothing
// listening there). Zero when none is logged.
func codexReached(dir string, since time.Time) (at time.Time, to string, refused bool) {
	logs, _ := filepath.Glob(filepath.Join(dir, "logs_*.sqlite"))
	if len(logs) == 0 {
		return
	}
	slices.Sort(logs)
	db, err := provider.OpenReadOnly(logs[len(logs)-1])
	if err != nil {
		return
	}
	defer db.Close()
	from := max(since.Unix(), time.Now().Add(-24*time.Hour).Unix())
	rows, err := db.Query(`SELECT ts, feedback_log_body FROM logs
		WHERE ts > ? AND target = 'codex_api::endpoint::responses_websocket'
		ORDER BY ts DESC, ts_nanos DESC, id DESC LIMIT 20`, from)
	if err != nil {
		return
	}
	defer rows.Close()
	// newest first: a refusal comes before the attempt it answers
	failedAt := map[string]bool{}
	for rows.Next() {
		var ts int64
		var body string
		if rows.Scan(&ts, &body) != nil {
			continue
		}
		if _, u, ok := strings.Cut(body, "failed to connect to websocket: "); ok {
			if _, u, ok := strings.Cut(u, "url: "); ok && (strings.Contains(body, "Connection refused") || strings.Contains(body, "os error 10061")) {
				failedAt[strings.TrimSpace(u)] = true
			}
			continue
		}
		if _, u, ok := strings.Cut(body, "connecting to websocket: "); ok {
			u = strings.TrimSpace(u)
			return time.Unix(ts, 0), u, failedAt[u]
		}
	}
	return
}
