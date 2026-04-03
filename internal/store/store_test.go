package store

import (
	"path/filepath"
	"testing"

	"github.com/guyuanshun/tmux-ghostty/internal/model"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := New(filepath.Join(dir, "state.json"), filepath.Join(dir, "actions.json"))
	state := model.NewState()
	workspace := model.NewWorkspace()
	pane := model.NewPane(workspace.ID)
	workspace.PaneIDs = append(workspace.PaneIDs, pane.ID)
	state.Workspaces[workspace.ID] = workspace
	state.Panes[pane.ID] = pane
	action := model.NewAction(pane.ID, "agent", "pwd", "pwd", model.RiskRead, model.ApprovalNotRequired, model.ActionSent)

	if err := st.SaveState(state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := st.SaveActions([]model.Action{action}); err != nil {
		t.Fatalf("save actions: %v", err)
	}

	gotState, err := st.LoadState()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	gotActions, err := st.LoadActions()
	if err != nil {
		t.Fatalf("load actions: %v", err)
	}

	if len(gotState.Workspaces) != 1 || len(gotState.Panes) != 1 {
		t.Fatalf("unexpected decoded state: %#v", gotState)
	}
	if len(gotActions) != 1 || gotActions[0].ID != action.ID {
		t.Fatalf("unexpected decoded actions: %#v", gotActions)
	}
}
