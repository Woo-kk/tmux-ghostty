package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/guyuanshun/tmux-ghostty/internal/model"
)

type Store struct {
	StatePath   string
	ActionsPath string
}

func New(statePath, actionsPath string) Store {
	return Store{
		StatePath:   statePath,
		ActionsPath: actionsPath,
	}
}

func (s Store) LoadState() (model.State, error) {
	if _, err := os.Stat(s.StatePath); os.IsNotExist(err) {
		return model.NewState(), nil
	}
	var state model.State
	if err := readJSONFile(s.StatePath, &state); err != nil {
		return model.State{}, fmt.Errorf("load state: %w", err)
	}
	if state.Workspaces == nil {
		state.Workspaces = map[string]model.Workspace{}
	}
	if state.Panes == nil {
		state.Panes = map[string]model.Pane{}
	}
	if state.Version == 0 {
		state.Version = model.StateVersion
	}
	return state, nil
}

func (s Store) SaveState(state model.State) error {
	state.Version = model.StateVersion
	if state.Workspaces == nil {
		state.Workspaces = map[string]model.Workspace{}
	}
	if state.Panes == nil {
		state.Panes = map[string]model.Pane{}
	}
	return writeJSONFile(s.StatePath, state)
}

func (s Store) LoadActions() ([]model.Action, error) {
	if _, err := os.Stat(s.ActionsPath); os.IsNotExist(err) {
		return []model.Action{}, nil
	}
	var actions []model.Action
	if err := readJSONFile(s.ActionsPath, &actions); err != nil {
		return nil, fmt.Errorf("load actions: %w", err)
	}
	return actions, nil
}

func (s Store) SaveActions(actions []model.Action) error {
	if actions == nil {
		actions = []model.Action{}
	}
	return writeJSONFile(s.ActionsPath, actions)
}

func readJSONFile(path string, dest any) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(buf) == 0 {
		return nil
	}
	return json.Unmarshal(buf, dest)
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(buf, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
