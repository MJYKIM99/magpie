package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestExternalCodexOwnershipNeverRefreshesOrWrites(t *testing.T) {
	for _, setting := range []string{"", "external", "typo"} {
		t.Run(setting, func(t *testing.T) {
			t.Setenv("MAGPIE_CODEX_AUTH_OWNER", setting)
			home := signIn(t)
			path := codexAuthFile(t, home, "external-refresh")
			before, _ := os.ReadFile(path)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			t.Cleanup(srv.Close)
			old := codexTokenURL
			codexTokenURL = srv.URL
			t.Cleanup(func() { codexTokenURL = old })
			if _, _, err := codexToken(context.Background(), path); !errors.Is(err, errCodexAuthExternal) {
				t.Fatalf("expected owner guidance, got %v", err)
			}
			rememberLogins(true)
			codexSignIn(t, home, "work@example.com", "other-refresh")
			rememberLogins(true)
			current, _ := os.ReadFile(path)
			if _, _, err := savedLoginToken(context.Background(), "codex", "me@example.com"); !errors.Is(err, errCodexAuthExternal) {
				t.Fatalf("saved copy refreshed: %v", err)
			}
			for _, r := range RenewLogins(context.Background(), 0) {
				if r.Agent == "codex" {
					t.Fatal("keepalive attempted external Codex renewal")
				}
			}
			if err := SwitchLogin("codex", "me@example.com"); !errors.Is(err, errCodexAuthExternal) {
				t.Fatalf("account switch allowed: %v", err)
			}
			if _, err := StartSignIn("codex"); !errors.Is(err, errCodexAuthExternal) {
				t.Fatalf("sign-in allowed: %v", err)
			}
			if _, err := addLogin(savedLogin{Agent: "codex", User: "other@example.com", Auth: before}); !errors.Is(err, errCodexAuthExternal) {
				t.Fatalf("login write allowed: %v", err)
			}
			after, _ := os.ReadFile(path)
			if string(current) != string(after) || calls.Load() != 0 {
				t.Fatal("external credential store or token endpoint touched")
			}
			for _, l := range readLogins() {
				if l.User == "me@example.com" {
					var original, saved map[string]any
					if json.Unmarshal(before, &original) != nil || json.Unmarshal(l.Auth, &saved) != nil || !reflect.DeepEqual(original, saved) || l.Lapsed != "" {
						t.Fatal("external saved login changed or marked lapsed")
					}
				}
			}
		})
	}
}

func TestExternalCodexReadsCurrentValidSignIn(t *testing.T) {
	t.Setenv("MAGPIE_CODEX_AUTH_OWNER", "")
	home := signIn(t)
	path := filepath.Join(home, ".codex", "auth.json")
	if tok, id, err := codexToken(context.Background(), path); err != nil || tok == "" || id != "acct-1" {
		t.Fatalf("initial login: %q %v", id, err)
	}
	codexSignIn(t, home, "new@example.com", "new-refresh")
	before, _ := os.ReadFile(path)
	if tok, id, err := codexToken(context.Background(), path); err != nil || tok == "" || id != "acct-new@example.com" {
		t.Fatalf("new login: %q %v", id, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("valid external sign-in changed")
	}
}

func TestManagedCodexRefreshReportsWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory write permissions")
	}
	t.Setenv("MAGPIE_CODEX_AUTH_OWNER", "magpie")
	home := signIn(t)
	path := codexAuthFile(t, home, "rt.1.spent")
	dir := filepath.Dir(path)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	codexTokenServer(t, "rt.1.spent", "ours", "rt.1.ours", func() {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Error(err)
		}
	})
	if _, _, err := codexToken(context.Background(), path); err == nil || !strings.Contains(err.Error(), "save refreshed Codex sign-in") {
		t.Fatalf("write failure hidden: %v", err)
	}
}
