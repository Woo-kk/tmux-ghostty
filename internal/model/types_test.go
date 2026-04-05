package model

import "testing"

func TestNewPaneDefaults(t *testing.T) {
	pane := NewPane("ws-test")
	if pane.Controller != ControllerUser {
		t.Fatalf("expected default controller user, got %q", pane.Controller)
	}
	if pane.Mode != ModeIdle {
		t.Fatalf("expected default mode idle, got %q", pane.Mode)
	}
	if pane.RemoteTmuxSession != "tmux-ghostty" {
		t.Fatalf("expected default remote tmux session")
	}
	if pane.LocalTmuxTarget != pane.LocalTmuxSession+":0.0" {
		t.Fatalf("expected default local tmux target to point at session root pane")
	}
	if !pane.OwnsLocalTmux {
		t.Fatalf("expected new pane to own its local tmux session")
	}
	if pane.Stage != StageUnknown {
		t.Fatalf("expected default stage unknown, got %q", pane.Stage)
	}
}

func TestNewWorkspaceDefaults(t *testing.T) {
	workspace := NewWorkspace()
	if workspace.Status != WorkspaceActive {
		t.Fatalf("expected default workspace status active, got %q", workspace.Status)
	}
	if workspace.LaunchMode != WorkspaceLaunchModeNewWindow {
		t.Fatalf("expected default workspace launch mode new_window, got %q", workspace.LaunchMode)
	}
}
