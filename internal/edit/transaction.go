package edit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WritePrivate writes sensitive state with owner-only permissions.
func WritePrivate(path string, data []byte) error {
	return writeAtomic(path, data, true)
}

// Transaction stages all edits before replacing the original configuration.
func Transaction(path string, change func(string) error) error {
	return TransactionFiles([]string{path}, func(paths []string) error { return change(paths[0]) })
}

// TransactionFiles stages configuration and recovery state together. A failed
// edit publishes nothing; a failed publication rolls back earlier writes.
// The last read detects other writers, but is not a cross-process lock.
func TransactionFiles(paths []string, change func([]string) error) error {
	read := func(path string) ([]byte, error) {
		b, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return b, err
	}
	dir, err := os.MkdirTemp("", "magpie-config-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	before := make([][]byte, len(paths))
	after := make([][]byte, len(paths))
	modes := make([]fs.FileMode, len(paths))
	scratch := make([]string, len(paths))
	for i, path := range paths {
		before[i], err = read(path)
		if err != nil {
			return err
		}
		folder := filepath.Join(dir, fmt.Sprint(i))
		if err := os.Mkdir(folder, 0o700); err != nil {
			return err
		}
		scratch[i] = filepath.Join(folder, filepath.Base(path))
		if before[i] != nil {
			st, err := os.Stat(path)
			if err != nil {
				return err
			}
			// The enclosing directory is private; retain the intended mode.
			if err := os.WriteFile(scratch[i], before[i], st.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	if err := change(scratch); err != nil {
		message := err.Error()
		for i, staged := range scratch {
			message = strings.ReplaceAll(message, staged, paths[i])
		}
		return transactionError{err, message}
	}
	for i, path := range paths {
		after[i], err = read(scratch[i])
		if err != nil {
			return err
		}
		if after[i] != nil {
			st, err := os.Stat(scratch[i])
			if err != nil {
				return err
			}
			modes[i] = st.Mode().Perm()
		}
		now, err := read(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(before[i], now) || (before[i] == nil) != (now == nil) {
			return fmt.Errorf("%s changed while magpie was editing it; retry after the other tool finishes", path)
		}
	}
	return Atomically(func() error {
		for i, path := range paths {
			if bytes.Equal(before[i], after[i]) && (before[i] == nil) == (after[i] == nil) {
				continue
			}
			if after[i] == nil {
				if err := Remove(path); err != nil {
					return err
				}
			} else if err := writeAtomic(path, after[i], modes[i] == 0o600); err != nil {
				return err
			}
		}
		return nil
	}, paths...)
}

type transactionError struct {
	err     error
	message string
}

func (e transactionError) Error() string { return e.message }
func (e transactionError) Unwrap() error { return e.err }
