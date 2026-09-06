package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct {
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Load() (persistedState, error) {
	state := persistedState{
		Version:  stateVersion,
		Settings: DefaultSettings(),
		Stacks:   make(map[string]StackState),
		Events:   make([]Event, 0),
	}

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read state: %w", err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("decode state: %w", err)
	}
	if state.Stacks == nil {
		state.Stacks = make(map[string]StackState)
	}
	if state.Events == nil {
		state.Events = make([]Event, 0)
	}
	if state.Settings.CheckTime == "" {
		state.Settings = DefaultSettings()
	}
	return state, nil
}

func (s *Store) Save(state persistedState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}
