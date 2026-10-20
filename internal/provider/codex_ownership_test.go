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
			if _, _, err := codexToken(context.Background(), filepath.Dir(path)); !errors.Is(err, errCodexAuthExternal) {
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
			live, ok := liveLogin("codex")
			if !ok {
				t.Fatal("current Codex sign-in missing")
			}
			if err := ForgetLogin("codex", live.User); !errors.Is(err, errCodexAuthExternal) {
				t.Fatalf("external sign-in deletion allowed: %v", err)
			}
			if _, err := StartSignInAt("codex", ""); !errors.Is(err, errCodexAuthExternal) {
				t.Fatalf("direct sign-in allowed: %v", err)
			}
			if _, err := StartSignIn("codex"); !errors.Is(err, errCodexAuthExternal) {
				t.Fatalf("sign-in allowed: %v", err)
			}
			if _, err := StartSignInWith("codex", SignInOptions{}); !errors.Is(err, errCodexAuthExternal) {
				t.Fatalf("sign-in with options allowed: %v", err)
			}
			if _, err := ImportLogins(context.Background(), "codex", []string{string(before)}); !errors.Is(err, errCodexAuthExternal) {
				t.Fatalf("account import allowed: %v", err)
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

func TestExternalCodexKeychainNeverRefreshesOrWrites(t *testing.T) {
	t.Setenv("MAGPIE_CODEX_AUTH_OWNER", "")
	home := signIn(t)
	codexAuthFile(t, home, "external-keychain-refresh")
	k, account := codexInKeychain(t, home, "keyring")
	before := append([]byte(nil), k.item(account)...)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	t.Cleanup(srv.Close)
	old := codexTokenURL
	codexTokenURL = srv.URL
	t.Cleanup(func() { codexTokenURL = old })
	if _, _, err := codexToken(context.Background(), filepath.Join(home, ".codex")); !errors.Is(err, errCodexAuthExternal) {
		t.Fatalf("keychain refresh did not respect external ownership: %v", err)
	}
	if calls.Load() != 0 || k.puts != 0 || k.removes != 0 || string(k.item(account)) != string(before) {
		t.Fatal("external keychain sign-in or token endpoint changed")
	}
}

func TestManagedCodexKeychainLeavesANewerSignInAlone(t *testing.T) {
	t.Setenv("MAGPIE_CODEX_AUTH_OWNER", "magpie")
	home := signIn(t)
	codexAuthFile(t, home, "rt.keychain.spent")
	k, account := codexInKeychain(t, home, "keyring")
	var newer map[string]any
	if err := json.Unmarshal(k.item(account), &newer); err != nil {
		t.Fatal(err)
	}
	newer["tokens"].(map[string]any)["refresh_token"] = "rt.keychain.other"
	changed, _ := json.Marshal(newer)
	codexTokenServer(t, "rt.keychain.spent", "ours", "rt.keychain.ours", func() {
		k.set(account, changed)
	})
	if tok, _, err := codexToken(context.Background(), filepath.Join(home, ".codex")); err == nil || tok != "" {
		t.Fatalf("a changed keychain sign-in was still used: %v", err)
	}
	if string(k.item(account)) != string(changed) || k.puts != 0 {
		t.Fatal("a newer keychain sign-in was overwritten")
	}
}

func TestExternalCodexReadsCurrentValidSignIn(t *testing.T) {
	t.Setenv("MAGPIE_CODEX_AUTH_OWNER", "")
	home := signIn(t)
	path := filepath.Join(home, ".codex", "auth.json")
	if tok, id, err := codexToken(context.Background(), filepath.Dir(path)); err != nil || tok == "" || id != "acct-1" {
		t.Fatalf("initial login: %q %v", id, err)
	}
	codexSignIn(t, home, "new@example.com", "new-refresh")
	before, _ := os.ReadFile(path)
	if tok, id, err := codexToken(context.Background(), filepath.Dir(path)); err != nil || tok == "" || id != "acct-new@example.com" {
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
	if _, _, err := codexToken(context.Background(), filepath.Dir(path)); err == nil || !strings.Contains(err.Error(), "save refreshed Codex sign-in") {
		t.Fatalf("write failure hidden: %v", err)
	}
}
