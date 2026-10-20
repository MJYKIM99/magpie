package edit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransactionKeepsOriginalOnFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	before := []byte("{\"theme\":\"dark\"}\n")
	if err := os.WriteFile(p, before, 0o600); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("second edit failed")
	err := Transaction(p, func(scratch string) error {
		if err := SetJSON(scratch, KV{Path: "model", Value: "new"}); err != nil {
			return err
		}
		return stop
	})
	after, _ := os.ReadFile(p)
	if !errors.Is(err, stop) || string(after) != string(before) {
		t.Fatalf("partial edit published: %q, %v", after, err)
	}
}

func TestTransactionDetectsAnotherWriter(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("model = \"old\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := []byte("model = \"other\"\n")
	err := Transaction(p, func(scratch string) error {
		if err := SetTOMLTop(scratch, KV{Path: "model", Value: "ours"}); err != nil {
			return err
		}
		return os.WriteFile(p, other, 0o600)
	})
	after, _ := os.ReadFile(p)
	if err == nil || string(after) != string(other) {
		t.Fatalf("other writer overwritten: %q, %v", after, err)
	}
}

func TestTOMLMissingKeyStaysOutsideManagedBlock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	src := "# mine\n# BEGIN another-router-managed\nopenai_base_url = \"old\"\n# END another-router-managed\n[other]\nvalue = 1\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetTOMLTop(p, KV{Path: "model_provider", Value: "custom"}, KV{Path: "openai_base_url", Value: "new"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Index(s, "model_provider") > strings.Index(s, "# BEGIN") || !strings.Contains(s, "# BEGIN another-router-managed\nopenai_base_url = \"new\"") {
		t.Fatalf("incorrect managed block boundaries: %s", s)
	}
}

func TestJSONInvalidInputIsNotRewritten(t *testing.T) {
	for _, change := range []func(string) error{
		func(p string) error { return SetJSON(p, KV{Path: "model", Value: "new"}) },
		func(p string) error { return DelJSON(p, "model") },
	} {
		p := filepath.Join(t.TempDir(), "settings.json")
		before := []byte(`{"model":"old", broken}`)
		if err := os.WriteFile(p, before, 0o600); err != nil {
			t.Fatal(err)
		}
		err := change(p)
		after, _ := os.ReadFile(p)
		if err == nil || string(after) != string(before) {
			t.Fatalf("damaged malformed config: %q, %v", after, err)
		}
	}
}
