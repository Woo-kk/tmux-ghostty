package jump

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
	if got := DetectStage(text); got != model.StageJumpMenu {
		t.Fatalf("DetectStage() = %q, want %q", got, model.StageJumpMenu)
	}

	text = "password:\nnoise\n[Host]>"
	if got := DetectStage(text); got != model.StageHostSearch {
		t.Fatalf("DetectStage() = %q, want %q", got, model.StageHostSearch)
	}

	text = "password:\nnoise\nroot@test:~$"
	if got := DetectStage(text); got != model.StageRemoteShell {
		t.Fatalf("DetectStage() = %q, want %q", got, model.StageRemoteShell)
	}
}

func TestAttachHostFallsBackToHostList(t *testing.T) {
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
	client := newTestJumpClient(t, controller)

	resolved, err := client.AttachHost("%1", "test4")
	if err != nil {
		t.Fatalf("AttachHost() error = %v", err)
	}
	if resolved.ResolvedVia != ResolvedViaHostListSearch {
		t.Fatalf("expected host_list_search fallback, got %q", resolved.ResolvedVia)
	}
	wantTrace := []model.PaneStage{
		model.StageJumpMenu,
		model.StageHostSearch,
		model.StageAccountSelect,
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
	if resolved.AccountID != "1" || resolved.AccountLabel != "root" {
		t.Fatalf("unexpected account selection: %+v", resolved)
	}
	if got := len(controller.sendHistory); got < 5 {
		t.Fatalf("expected multiple jump inputs, got %v", controller.sendHistory)
	}
}

func TestAttachHostReturnsStructuredAmbiguityWithoutRoot(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"Opt>",
			"1 | admin | Admin\n2 | ops | Ops\nID>",
		},
	}
	client := newTestJumpClient(t, controller)

	_, err := client.AttachHost("%1", "2801")
	if err == nil {
		t.Fatalf("expected ambiguity error")
	}
	var attachErr *AttachError
	if !errors.As(err, &attachErr) {
		t.Fatalf("expected AttachError, got %T", err)
	}
	if attachErr.Reason != AttachReasonAccountAmbiguous {
		t.Fatalf("unexpected attach error reason: %q", attachErr.Reason)
	}
	if len(attachErr.Candidates) != 2 {
		t.Fatalf("expected candidate list, got %+v", attachErr.Candidates)
	}
}

func newTestJumpClient(t *testing.T, controller *fakeTmuxController) *Client {
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
		tmux:          controller,
		profilePath:   profilePath,
		runnerScript:  runnerPath,
		remoteSession: defaultRemoteSession,
	}
}
