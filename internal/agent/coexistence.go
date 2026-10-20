package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/yetone/magpie/internal/edit"
)

// recoveryState gives the upstream adapters a private, checked stash while
// their configuration is staged. Their existing stash operations stay intact.
type recoveryState struct {
	path   string
	err    error
	notice string
}

func (s *recoveryState) load() map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return m
	}
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	if err == nil && m == nil {
		err = errors.New("magpie recovery state must be an object")
	}
	if err != nil {
		s.err = errors.Join(s.err, fmt.Errorf("read magpie recovery state: %w", err))
		return map[string]string{}
	}
	return m
}

func (s *recoveryState) set(values map[string]string) {
	m := s.load()
	if s.err != nil {
		return
	}
	for k, v := range values {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err == nil {
		err = edit.WritePrivate(s.path, b)
	}
	s.err = errors.Join(s.err, err)
}

func (s *recoveryState) forget(keys ...string) {
	values := map[string]string{}
	for _, k := range keys {
		values[k] = ""
	}
	s.set(values)
}

func (s *recoveryState) take(key string) string {
	v := s.load()[key]
	if v != "" {
		s.forget(key)
	}
	return v
}

// coexistence runs the current upstream adapter on scratch files, so readers
// never see a partial config and failed edits retain their recovery records.
// The stash is the last path. The rebuilt adapter has no transaction wrapper.
func coexistence(a *Agent, paths []string, current *recoveryState, build func([]string, *recoveryState) *Agent) *Agent {
	run := func(change func(*Agent) error) error {
		var stagedNotice string
		err := edit.TransactionFiles(paths, func(scratch []string) error {
			state := &recoveryState{path: scratch[len(scratch)-1], notice: current.notice}
			state.load()
			if state.err != nil {
				return state.err
			}
			err := change(build(scratch, state))
			stagedNotice = state.notice
			return errors.Join(err, state.err)
		})
		if err == nil {
			current.notice = stagedNotice
		}
		return err
	}
	for i := range a.Fields {
		key := a.Fields[i].Key
		if a.Fields[i].Set != nil {
			a.Fields[i].Set = func(v string) error {
				return run(func(staged *Agent) error { return staged.Field(key).Set(v) })
			}
		}
	}
	if a.Sync != nil {
		a.Sync = func() error { return run(func(staged *Agent) error { return staged.Sync() }) }
	}
	if a.Follow != nil {
		a.Follow = func() error { return run(func(staged *Agent) error { return staged.Follow() }) }
	}
	if a.Unwire != nil {
		a.Unwire = func() error { return run(func(staged *Agent) error { return staged.Unwire() }) }
	}
	if a.Join != nil {
		a.Join = func() (joined bool, err error) {
			err = run(func(staged *Agent) error {
				joined, err = staged.Join()
				return err
			})
			return joined && err == nil, err
		}
	}
	return a
}
