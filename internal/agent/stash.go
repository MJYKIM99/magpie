package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

// The stash remembers what an agent's config said before magpie pointed it
// at the gateway, so switching back restores it instead of guessing.

func stashPath() string { return filepath.Join(filepath.Dir(provider.Path()), "stash.json") }

func stashLoad() map[string]string {
	out := map[string]string{}
	if b, err := os.ReadFile(stashPath()); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

// Coexistence adapters must keep their recovery data until configuration
// changes succeed, and must not take over if that data cannot be saved.
func stashRead() (map[string]string, error) {
	m := map[string]string{}
	b, err := edit.Read(stashPath())
	if err != nil {
		return nil, err
	}
	if len(b) != 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("read magpie recovery state: %w", err)
		}
	}
	if m == nil {
		return nil, fmt.Errorf("magpie recovery state must be an object")
	}
	return m, nil
}

func stashUpdate(values map[string]string, remove ...string) error {
	m, err := stashRead()
	if err != nil {
		return err
	}
	for _, k := range remove {
		delete(m, k)
	}
	for k, v := range values {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return edit.WritePrivate(stashPath(), b)
}

func stash(kv map[string]string) {
	m := stashLoad()
	for k, v := range kv {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	os.MkdirAll(filepath.Dir(stashPath()), 0o755)
	os.WriteFile(stashPath(), b, 0o600)
}

// forget drops stashed values without restoring them.
func forget(keys ...string) {
	m := stashLoad()
	for _, k := range keys {
		delete(m, k)
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(stashPath(), b, 0o600)
}

func unstash(key string) string {
	m := stashLoad()
	v := m[key]
	if v != "" {
		delete(m, key)
		b, _ := json.MarshalIndent(m, "", "  ")
		os.WriteFile(stashPath(), b, 0o600)
	}
	return v
}
