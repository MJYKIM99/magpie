package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

func codexHome(t *testing.T, auth, config string) (home string, read func() string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEX_HOME", "")
	noKeychain(t)
	usedUp := codexUsedUp
	codexUsedUp = func() bool { return false }
	t.Cleanup(func() { codexUsedUp = usedUp })
	dir := filepath.Join(home, ".codex")
	os.MkdirAll(dir, 0o755)
	if auth != "" {
		os.WriteFile(filepath.Join(dir, "auth.json"), []byte(auth), 0o600)
	}
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o644)
	if err := provider.Save(provider.Provider{ID: "fake", Name: "Fake", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"m1"}}); err != nil {
		t.Fatal(err)
	}
	return home, func() string {
		b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
		return string(b)
	}
}

// Signed in, a magpie model points Codex's built-in OpenAI provider at
// magpie and leaves the provider as it is; one of Codex's own models takes
// the base URL away again and brings back what was there.
func TestCodexSignedInRoutesByBaseURL(t *testing.T) {
	home, read := codexHome(t, `{"tokens":{"access_token":"x","id_token":"x.e30.x"}}`,
		"model = \"gpt-5.5\"\nmodel_reasoning_effort = \"xhigh\"\n\n[projects.\"/x\"]\ntrust_level = \"trusted\"\n")
	cx := codex(home)
	if err := cx.Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	cfg := read()
	if !strings.Contains(cfg, `openai_base_url = "http://127.0.0.1:`) || !strings.Contains(cfg, `/backend-api/codex"`) ||
		!strings.Contains(cfg, `model = "fake/m1"`) || strings.Contains(cfg, "model_provider") ||
		strings.Contains(cfg, "model_catalog_json") || strings.Contains(cfg, "[model_providers") {
		t.Fatalf("magpie model:\n%s", cfg)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "magpie-models.json")); err == nil {
		t.Error("catalog file written")
	}
	if err := cx.Fields[0].Set("gpt-5.4"); err != nil {
		t.Fatal(err)
	}
	cfg = read()
	if strings.Contains(cfg, "openai_base_url") || !strings.Contains(cfg, `model = "gpt-5.4"`) ||
		!strings.Contains(cfg, `model_reasoning_effort = "xhigh"`) || !strings.Contains(cfg, `trust_level = "trusted"`) {
		t.Fatalf("own model:\n%s", cfg)
	}
}

// A magpie set up as a provider of Codex's, from before, moves to the base
// URL the next time a magpie model is picked.
func TestCodexSignedInLeavesProviderTable(t *testing.T) {
	home, read := codexHome(t, `{"OPENAI_API_KEY":"sk-x"}`,
		"model = \"fake/m1\"\nmodel_provider = \"magpie\"\nmodel_catalog_json = \"/x/magpie-models.json\"\n\n[model_providers.magpie]\nname = \"magpie\"\nbase_url = \"http://127.0.0.1:3425/v1\"\nwire_api = \"responses\"\n")
	if err := codex(home).Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	cfg := read()
	if !strings.Contains(cfg, "openai_base_url") || strings.Contains(cfg, "model_provider") || strings.Contains(cfg, "model_catalog_json") {
		t.Fatalf("\n%s", cfg)
	}
}

// Not signed in, Codex's OpenAI provider can't run, so magpie is a provider
// of its own.
func TestCodexSignedOutUsesProvider(t *testing.T) {
	home, read := codexHome(t, "", "")
	cx := codex(home)
	if err := cx.Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	cfg := read()
	if strings.Contains(cfg, "openai_base_url") || !strings.Contains(cfg, `model_provider = "magpie"`) ||
		!strings.Contains(cfg, "[model_providers.magpie]") || !strings.Contains(cfg, "model_catalog_json") {
		t.Fatalf("\n%s", cfg)
	}
	if err := cx.Fields[0].Set(""); err != nil {
		t.Fatal(err)
	}
	if cfg = read(); strings.Contains(cfg, "magpie") || strings.Contains(cfg, "model") {
		t.Fatalf("reset:\n%s", cfg)
	}
}

// A ChatGPT account that has used its allowance up keeps the Codex app from
// sending, so magpie is a provider of Codex's then, as when signed out.
func TestCodexUsedUpUsesProvider(t *testing.T) {
	home, read := codexHome(t, `{"tokens":{"access_token":"x","id_token":"x.e30.x"}}`, "")
	codexUsedUp = func() bool { return true }
	if err := codex(home).Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	cfg := read()
	if strings.Contains(cfg, "openai_base_url") || !strings.Contains(cfg, `model_provider = "magpie"`) ||
		!strings.Contains(cfg, "[model_providers.magpie]") || !strings.Contains(cfg, `model = "fake/m1"`) {
		t.Fatalf("\n%s", cfg)
	}
}

// Subagents can be given a magpie model of their own once Codex runs through
// magpie; it goes with magpie when Codex steps back to its own models, where
// a user's own choice of one of those stays.
func TestCodexSubagentModel(t *testing.T) {
	home, read := codexHome(t, `{"tokens":{"access_token":"x","id_token":"x.e30.x"}}`,
		"model = \"gpt-5.5\"\n\n[agents]\nmax_threads = 6\n")
	cx := codex(home)
	sub := cx.Field("subagent")
	if sub == nil {
		t.Fatal("no subagent field")
	}
	if err := sub.Set("fake/m1"); err == nil {
		t.Error("a magpie model was taken before Codex runs through magpie")
	}
	for _, o := range sub.Options(nil) {
		if isMagpie(o.Value) {
			t.Errorf("magpie model offered before Codex runs through magpie: %s", o.Value)
		}
	}
	if err := cx.Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	if err := sub.Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	if got := sub.Get(); got != "fake/m1" {
		t.Fatalf("subagent = %q\n%s", got, read())
	}
	if cfg := read(); !strings.Contains(cfg, "max_threads = 6") {
		t.Fatalf("user's [agents] key lost:\n%s", cfg)
	}
	if err := cx.Fields[0].Set("gpt-5.4"); err != nil {
		t.Fatal(err)
	}
	if cfg := read(); strings.Contains(cfg, "default_subagent_model") || !strings.Contains(cfg, "max_threads = 6") {
		t.Fatalf("stepping out of magpie:\n%s", cfg)
	}
	if err := sub.Set("gpt-5.4-mini"); err != nil {
		t.Fatal(err)
	}
	if err := cx.Fields[0].Set(""); err != nil {
		t.Fatal(err)
	}
	if got := sub.Get(); got != "gpt-5.4-mini" {
		t.Fatalf("own subagent model dropped: %q", got)
	}
	if err := sub.Set(""); err != nil {
		t.Fatal(err)
	}
	if cfg := read(); strings.Contains(cfg, "default_subagent_model") {
		t.Fatalf("\n%s", cfg)
	}
}

// routerConfig is what another local gateway leaves in Codex's config: its
// own marked block with the endpoint it serves and the catalog behind it,
// and the provider Codex is told to use. The order of the lines matches a
// real codex-router install, so a rewrite that moved them would show here.
const routerConfig = `model_provider = "custom"
model = "opencode-go/deepseek-v4.1-flash"
model_reasoning_effort = "max"

# BEGIN codex-router-managed
openai_base_url = "http://127.0.0.1:4202/v1"
model_catalog_json = "/tmp/another-router/merged-models.json"
# END codex-router-managed

[model_providers.custom]
name = "Another Router"
base_url = "http://127.0.0.1:4202/v1"
wire_api = "responses"
requires_openai_auth = true
`

const routerAuth = `{"tokens":{"access_token":"x","id_token":"x.e30.x"}}`

// top reads one top-level key of the scratch home's config.toml.
func top(t *testing.T, home, key string) string {
	t.Helper()
	v, _ := edit.GetTOMLTop(filepath.Join(home, ".codex", "config.toml"), key)
	return v
}

// Coexistence: magpie takes Codex's endpoint over from another local
// gateway while one of its models is selected, and hands that gateway's
// config back — endpoint, provider, catalog and all — when the user picks
// one of Codex's own models again.
func TestCodexHandsBackAnotherRoutersConfig(t *testing.T) {
	home, read := codexHome(t, routerAuth, routerConfig)
	cx := codex(home)

	if err := cx.Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	cfg := read()
	if got := top(t, home, "openai_base_url"); !isCodexGateway(got) {
		t.Fatalf("magpie did not take the endpoint over: %q\n%s", got, cfg)
	}
	if got := top(t, home, "model_provider"); got != "" {
		t.Errorf("model_provider kept while magpie serves the endpoint, so Codex would not reach magpie: %q", got)
	}
	if !strings.Contains(cfg, "# BEGIN codex-router-managed") || !strings.Contains(cfg, "[model_providers.custom]") {
		t.Errorf("another router's own lines were disturbed:\n%s", cfg)
	}
	if n := strings.Count(cfg, "openai_base_url"); n != 1 {
		t.Errorf("%d base URLs while magpie holds it, want 1:\n%s", n, cfg)
	}

	if err := cx.Fields[0].Set("gpt-5.4"); err != nil {
		t.Fatal(err)
	}
	cfg = read()
	for key, want := range map[string]string{
		"openai_base_url":        "http://127.0.0.1:4202/v1",
		"model_provider":         "custom",
		"model_catalog_json":     "/tmp/another-router/merged-models.json",
		"model_reasoning_effort": "max",
		"model":                  "gpt-5.4",
	} {
		if got := top(t, home, key); got != want {
			t.Errorf("stepping out: %s = %q, want %q\n%s", key, got, want, cfg)
		}
	}
	if n := strings.Count(cfg, "openai_base_url"); n != 1 {
		t.Errorf("%d base URLs after stepping out, want 1:\n%s", n, cfg)
	}
	if !strings.Contains(cfg, "[model_providers.custom]") {
		t.Errorf("another router's provider table lost:\n%s", cfg)
	}
	if strings.Contains(cfg, "magpie") {
		t.Errorf("magpie left something of its own behind:\n%s", cfg)
	}

	// and it holds up over a second round trip: the stash is taken once per
	// takeover and emptied on the way out, so nothing goes stale
	if err := cx.Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	if err := cx.Fields[0].Set("gpt-5.4"); err != nil {
		t.Fatal(err)
	}
	if got := top(t, home, "openai_base_url"); got != "http://127.0.0.1:4202/v1" {
		t.Fatalf("second round trip: openai_base_url = %q\n%s", got, read())
	}
}

// Asking for the agent's own default takes magpie out of Codex, and what
// was there before magpie — another router's endpoint above all — goes back
// rather than staying deleted.
func TestCodexDefaultHandsBackAnotherRoutersConfig(t *testing.T) {
	home, read := codexHome(t, routerAuth, routerConfig)
	cx := codex(home)
	if err := cx.Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	if err := cx.Fields[0].Set(""); err != nil {
		t.Fatal(err)
	}
	cfg := read()
	if strings.Contains(cfg, "[model_providers.magpie]") {
		t.Errorf("magpie's own provider table left behind:\n%s", cfg)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "magpie-models.json")); err == nil {
		t.Error("magpie's catalog file left behind")
	}
	// the endpoint, the provider and the catalog another program had are
	// handed back; the model is the field being reset, so it stays removed
	for key, want := range map[string]string{
		"openai_base_url":    "http://127.0.0.1:4202/v1",
		"model_provider":     "custom",
		"model_catalog_json": "/tmp/another-router/merged-models.json",
	} {
		if got := top(t, home, key); got != want {
			t.Errorf("reset: %s = %q, want %q\n%s", key, got, want, cfg)
		}
	}
	if got := top(t, home, "model"); got != "" {
		t.Errorf("reset left a model behind: %q", got)
	}
}

// Without another gateway there is nothing of the kind to hand back, and a
// reset still leaves Codex as installed.
func TestCodexResetWithNoOtherGateway(t *testing.T) {
	home, read := codexHome(t, routerAuth, "model = \"gpt-5.5\"\n")
	cx := codex(home)
	if err := cx.Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	if err := cx.Fields[0].Set(""); err != nil {
		t.Fatal(err)
	}
	if cfg := read(); strings.Contains(cfg, "openai_base_url") || strings.Contains(cfg, "model_provider") ||
		strings.Contains(cfg, "model") || strings.Contains(cfg, "magpie") {
		t.Fatalf("reset left something behind:\n%s", cfg)
	}
}

// A notice says when magpie has taken the endpoint over from another
// program on this machine, and stops saying it once it is handed back.
func TestCodexNoticeNamesTheDisplacedGateway(t *testing.T) {
	home, _ := codexHome(t, routerAuth, routerConfig)
	cx := codex(home)
	if n := cx.Notice(); strings.Contains(n, "4202") {
		t.Fatalf("notice before magpie holds the endpoint: %q", n)
	}
	if err := cx.Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	if n := cx.Notice(); !strings.Contains(n, "http://127.0.0.1:4202/v1") {
		t.Fatalf("notice does not name the gateway the endpoint came from: %q", n)
	}
	if err := cx.Fields[0].Set("gpt-5.4"); err != nil {
		t.Fatal(err)
	}
	if n := cx.Notice(); strings.Contains(n, "4202") {
		t.Fatalf("notice after the endpoint went back: %q", n)
	}
}

func TestForeignGateway(t *testing.T) {
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"", false},
		{"https://api.openai.com/v1", false},
		{"https://api.anthropic.com", false},
		{"http://127.0.0.1:3425/backend-api/codex", false}, // magpie's own
		{"http://127.0.0.1:3425/v1", false},                // magpie's own, the provider route
		{"http://127.0.0.1:4202/v1", true},
		{"http://localhost:8080/v1", true},
		{"http://[::1]:9000/v1", true},
		{"not a url", false},
	} {
		if got := foreignGateway(c.url); got != c.want {
			t.Errorf("foreignGateway(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}
