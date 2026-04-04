package control

import (
	"testing"

	"github.com/Woo-kk/tmux-ghostty/internal/model"
)

func TestClaimReleaseObserve(t *testing.T) {
	pane := model.NewPane("ws-1")

	pane = Claim(pane, model.ControllerAgent)
	if pane.Controller != model.ControllerAgent {
		t.Fatalf("expected controller agent, got %q", pane.Controller)
	}

	pane.Mode = model.ModeAwaitingApproval
	pane = Release(pane)
	if pane.Controller != model.ControllerUser {
		t.Fatalf("expected controller user, got %q", pane.Controller)
	}
	if pane.Mode != model.ModeIdle {
		t.Fatalf("expected release to reset awaiting approval to idle, got %q", pane.Mode)
	}

	pane = Observe(pane)
	if pane.Mode != model.ModeObserveOnly {
		t.Fatalf("expected observe_only mode, got %q", pane.Mode)
	}
}
