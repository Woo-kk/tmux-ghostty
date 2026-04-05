package remote

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/model"
)

type fakeTmuxController struct {
	snapshots   []string
	sendHistory []string
}

func (f *fakeTmuxController) SendKeys(target string, text string) error {
	f.sendHistory = append(f.sendHistory, text)
	return nil
}

func (f *fakeTmuxController) SendCtrlC(target string) error {
	return nil
}

func (f *fakeTmuxController) CapturePane(target string, lines int) (string, error) {
	if len(f.snapshots) == 0 {
		return "", errors.New("no snapshot queued")
	}
	value := f.snapshots[0]
	if len(f.snapshots) > 1 {
		f.snapshots = f.snapshots[1:]
	}
	return value, nil
}

func TestDetectStageUsesRecentPromptInsteadOfHistoricalPassword(t *testing.T) {
	text := "password:\nold prompt\nOpt>"
	if got := DetectStage(text); got != model.StageMenu {
		t.Fatalf("DetectStage() = %q, want %q", got, model.StageMenu)
	}

	text = "password:\nnoise\n[Host]>"
	if got := DetectStage(text); got != model.StageTargetSearch {
		t.Fatalf("DetectStage() = %q, want %q", got, model.StageTargetSearch)
	}

	text = "password:\nnoise\nroot@test:~$"
	if got := DetectStage(text); got != model.StageRemoteShell {
		t.Fatalf("DetectStage() = %q, want %q", got, model.StageRemoteShell)
	}

	text = "password:\n1 | root | Root Account\nID>"
	if got := DetectStage(text); got != model.StageSelection {
		t.Fatalf("DetectStage() = %q, want %q", got, model.StageSelection)
	}
}

func TestAttachTargetFallsBackToTargetList(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"Opt>",
			"No Assets\nOpt>",
			"[Host]>",
			"1 | root | Root Account\nID>",
			"资产[test4(10.0.0.4)]\nroot@test4:~$",
			"资产[test4(10.0.0.4)]\nroot@test4:~$",
		},
	}
	client := newTestRemoteClient(t, controller)

	resolved, err := client.AttachTarget("%1", "test4")
	if err != nil {
		t.Fatalf("AttachTarget() error = %v", err)
	}
	if resolved.ResolvedVia != ResolvedViaTargetListSearch {
		t.Fatalf("expected target_list_search fallback, got %q", resolved.ResolvedVia)
	}
	wantTrace := []model.PaneStage{
		model.StageMenu,
		model.StageTargetSearch,
		model.StageSelection,
		model.StageRemoteShell,
	}
	if len(resolved.StageTrace) != len(wantTrace) {
		t.Fatalf("unexpected stage trace length: %#v", resolved.StageTrace)
	}
	for index, stage := range wantTrace {
		if resolved.StageTrace[index] != stage {
			t.Fatalf("stage trace[%d] = %q, want %q", index, resolved.StageTrace[index], stage)
		}
	}
	if resolved.SelectionID != "1" || resolved.SelectionLabel != "root" {
		t.Fatalf("unexpected selection result: %+v", resolved)
	}
	if resolved.Provider != ProviderJumpServer {
		t.Fatalf("unexpected provider: %q", resolved.Provider)
	}
	if got := len(controller.sendHistory); got < 5 {
		t.Fatalf("expected multiple jump inputs, got %v", controller.sendHistory)
	}
}

func TestAttachTargetIgnoresTransientAuthPrompt(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"password:",
			"Opt>",
			"1 | root | Root Account\nID>",
			"资产[test4(10.0.0.4)]\nroot@test4:~$",
			"资产[test4(10.0.0.4)]\nroot@test4:~$",
		},
	}
	client := newTestRemoteClient(t, controller)

	resolved, err := client.AttachTarget("%1", "2801")
	if err != nil {
		t.Fatalf("AttachTarget() error = %v", err)
	}
	wantTrace := []model.PaneStage{
		model.StageMenu,
		model.StageSelection,
		model.StageRemoteShell,
	}
	if len(resolved.StageTrace) != len(wantTrace) {
		t.Fatalf("unexpected stage trace length: %#v", resolved.StageTrace)
	}
	for index, stage := range wantTrace {
		if resolved.StageTrace[index] != stage {
			t.Fatalf("stage trace[%d] = %q, want %q", index, resolved.StageTrace[index], stage)
		}
	}
	if resolved.ResolvedVia != ResolvedViaDirectQuery {
		t.Fatalf("expected direct query resolution, got %q", resolved.ResolvedVia)
	}
}

func TestAttachTargetReturnsStructuredAmbiguityWithoutRoot(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"Opt>",
			"1 | admin | Admin\n2 | ops | Ops\nID>",
		},
	}
	client := newTestRemoteClient(t, controller)

	_, err := client.AttachTarget("%1", "2801")
	if err == nil {
		t.Fatalf("expected ambiguity error")
	}
	var attachErr *AttachError
	if !errors.As(err, &attachErr) {
		t.Fatalf("expected AttachError, got %T", err)
	}
	if attachErr.Reason != AttachReasonSelectionAmbiguous {
		t.Fatalf("unexpected attach error reason: %q", attachErr.Reason)
	}
	if len(attachErr.Candidates) != 2 {
		t.Fatalf("expected candidate list, got %+v", attachErr.Candidates)
	}
}

func TestWaitForStageReturnsLastStableStageOnTimeout(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"Connecting to test4",
		},
	}
	client := newTestRemoteClient(t, controller)
	provider, ok := client.provider.(*jumpServerProvider)
	if !ok {
		t.Fatalf("expected jumpServerProvider, got %T", client.provider)
	}

	_, stage, err := provider.waitForStage("%1", 10*time.Millisecond, model.StageRemoteShell)
	if err == nil {
		t.Fatalf("expected waitForStage to time out")
	}
	if stage != model.StageConnecting {
		t.Fatalf("waitForStage() stage = %q, want %q", stage, model.StageConnecting)
	}
}

func newTestRemoteClient(t *testing.T, controller *fakeTmuxController) *Client {
	t.Helper()
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "default.env")
	runnerPath := filepath.Join(dir, "run_jump_profile.sh")
	if err := os.WriteFile(profilePath, []byte("profile=test\n"), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	if err := os.WriteFile(runnerPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}
	return &Client{
		provider: &jumpServerProvider{
			tmux:          controller,
			profilePath:   profilePath,
			runnerScript:  runnerPath,
			remoteSession: defaultRemoteSession,
		},
	}
}
