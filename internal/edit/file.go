// Package edit provides format-preserving editors for the config files that
// coding agents keep: JSON/JSONC, TOML and YAML. Only the requested key
// changes; comments, ordering and indentation are left as they are.
package edit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Read returns the file contents, or (nil, nil) when the file does not exist.
func Read(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// WriteAtomic writes data to path via a temp file + rename so a crash can
// never leave a half-written config behind. File mode is preserved.
func WriteAtomic(path string, data []byte) error {
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	return writeAtomic(path, data, mode)
}

// WritePrivate replaces sensitive state with owner-only permissions.
func WritePrivate(path string, data []byte) error {
	return writeAtomic(path, data, 0o600)
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Transaction applies a sequence of editors to a private scratch file and
// publishes only the final result. Errors leave the original untouched.
// The last read detects intervening edits, but is not a cross-process lock:
// independent configuration managers still need to be used sequentially.
func Transaction(path string, change func(string) error) error {
	before, err := Read(path)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "magpie-config-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	scratch := filepath.Join(dir, filepath.Base(path))
	if before != nil {
		if err := os.WriteFile(scratch, before, 0o600); err != nil {
			return err
		}
	}
	if err := change(scratch); err != nil {
		return err
	}
	after, err := Read(scratch)
	if err != nil || bytes.Equal(before, after) {
		return err
	}
	now, err := Read(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, now) || (before == nil) != (now == nil) {
		return fmt.Errorf("%s changed while magpie was editing it; retry after the other tool finishes", path)
	}
	return WriteAtomic(path, after)
}
