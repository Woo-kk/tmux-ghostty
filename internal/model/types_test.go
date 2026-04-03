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
}
