package remote

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/model"
)

type fakeTmuxController struct {
	snapshots      []string
	sendHistory    []string
	emptySendCount int
	sendHook       func(string)
}

func (f *fakeTmuxController) SendKeys(target string, text string) error {
	if text == "" {
		f.emptySendCount++
	} else {
		f.sendHistory = append(f.sendHistory, text)
	}
	if f.sendHook != nil {
		f.sendHook(text)
	}
	return nil
}

func (f *fakeTmuxController) SendText(target string, text string) error {
	f.sendHistory = append(f.sendHistory, text)
	if f.sendHook != nil {
		f.sendHook(text)
	}
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

func TestConnectTargetStopsAtMenu(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"Opt>",
			"Opt>",
		},
	}
	client := newTestRemoteClient(t, controller)

	connected, err := client.ConnectTarget("%1")
	if err != nil {
		t.Fatalf("ConnectTarget() error = %v", err)
	}
	if connected.Provider != ProviderJumpServer {
		t.Fatalf("unexpected provider: %q", connected.Provider)
	}
	if connected.Stage != model.StageMenu {
		t.Fatalf("ConnectTarget() stage = %q, want %q", connected.Stage, model.StageMenu)
	}
	if !connected.ReadyForUserInput {
		t.Fatalf("expected connect result to be ready for user input")
	}
	wantTrace := []model.PaneStage{model.StageMenu}
	if len(connected.StageTrace) != len(wantTrace) || connected.StageTrace[0] != wantTrace[0] {
		t.Fatalf("unexpected stage trace: %#v", connected.StageTrace)
	}
	if len(controller.sendHistory) != 1 {
		t.Fatalf("expected only profile start command, got %v", controller.sendHistory)
	}
}

func TestConnectTargetStopsAtTargetSearch(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"password:",
			"[Host]>",
			"[Host]>",
		},
	}
	client := newTestRemoteClient(t, controller)

	connected, err := client.ConnectTarget("%1")
	if err != nil {
		t.Fatalf("ConnectTarget() error = %v", err)
	}
	if connected.Stage != model.StageTargetSearch {
		t.Fatalf("ConnectTarget() stage = %q, want %q", connected.Stage, model.StageTargetSearch)
	}
	if !connected.ReadyForUserInput {
		t.Fatalf("expected connect result to be ready for user input")
	}
	wantTrace := []model.PaneStage{model.StageTargetSearch}
	if len(connected.StageTrace) != len(wantTrace) || connected.StageTrace[0] != wantTrace[0] {
		t.Fatalf("unexpected stage trace: %#v", connected.StageTrace)
	}
}

func TestConnectTargetReturnsAuthPromptAsReadyForUserInput(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"password:",
			"password:",
		},
	}
	client := newTestRemoteClient(t, controller)

	connected, err := client.ConnectTarget("%1")
	if err != nil {
		t.Fatalf("ConnectTarget() error = %v", err)
	}
	if connected.Stage != model.StageAuthPrompt {
		t.Fatalf("ConnectTarget() stage = %q, want %q", connected.Stage, model.StageAuthPrompt)
	}
	if !connected.ReadyForUserInput {
		t.Fatalf("expected auth prompt to be returned as ready for user input")
	}
	wantTrace := []model.PaneStage{model.StageAuthPrompt}
	if len(connected.StageTrace) != len(wantTrace) || connected.StageTrace[0] != wantTrace[0] {
		t.Fatalf("unexpected stage trace: %#v", connected.StageTrace)
	}
}

func TestAttachTargetEntersHostListBeforeSearchingFromMenu(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"Opt>",
			"Opt>",
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
	if len(controller.sendHistory) != 5 {
		t.Fatalf("unexpected jump input count: %v", controller.sendHistory)
	}
	if controller.sendHistory[1] != "h" {
		t.Fatalf("expected host-list navigation before query, got %v", controller.sendHistory)
	}
	if controller.sendHistory[2] != "test4" {
		t.Fatalf("expected clean host query after menu navigation, got %v", controller.sendHistory)
	}
}

func TestAttachTargetIgnoresTransientAuthPrompt(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"password:",
			"[Host]>",
			"Search:\n[Host]>",
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
	if resolved.ResolvedVia != ResolvedViaDirectQuery {
		t.Fatalf("expected direct query resolution, got %q", resolved.ResolvedVia)
	}
}

func TestAttachTargetReturnsStructuredAmbiguityWithoutRoot(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"Opt>",
			"Opt>",
			"[Host]>",
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

func TestEnterHostListWaitsForPromptEchoBeforeConfirming(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"Opt>",
			"Opt> h",
			"[Host]>",
		},
	}
	client := newTestRemoteClient(t, controller)
	provider, ok := client.provider.(*jumpServerProvider)
	if !ok {
		t.Fatalf("expected jumpServerProvider, got %T", client.provider)
	}

	snapshot, stage, err := provider.enterHostList("%1", model.StageMenu, nil)
	if err != nil {
		t.Fatalf("enterHostList() error = %v", err)
	}
	if stage != model.StageTargetSearch {
		t.Fatalf("enterHostList() stage = %q, want %q", stage, model.StageTargetSearch)
	}
	if !strings.Contains(snapshot, "[Host]>") {
		t.Fatalf("unexpected host list snapshot: %q", snapshot)
	}
	if controller.emptySendCount != 1 {
		t.Fatalf("expected exactly one confirm enter, got %d", controller.emptySendCount)
	}
	if len(controller.sendHistory) != 1 || controller.sendHistory[0] != "h" {
		t.Fatalf("expected host-list navigation to send only \"h\" before confirm, got %v", controller.sendHistory)
	}
}

func TestAttachTargetMarksRemoteTmuxUnavailableWithoutFailingAttach(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"[Host]>",
			"1 | root | Root Account\nID>",
			"资产[test4(10.0.0.4)]\nroot@test4:~$",
		},
		sendHook: func(text string) {},
	}
	controller.sendHook = func(text string) {
		marker := extractRemoteTmuxMarker(text)
		if marker == "" {
			return
		}
		controller.snapshots = append(controller.snapshots, marker+"\n"+marker+" unavailable tmux not found\nroot@test4:~$")
	}
	client := newTestRemoteClient(t, controller)

	resolved, err := client.AttachTarget("%1", "2801")
	if err != nil {
		t.Fatalf("AttachTarget() error = %v", err)
	}
	if resolved.RemoteTmuxStatus != model.RemoteTmuxStatusUnavailable {
		t.Fatalf("expected remote tmux unavailable, got %q", resolved.RemoteTmuxStatus)
	}
	if !strings.Contains(resolved.RemoteTmuxDetail, "tmux not found") {
		t.Fatalf("unexpected remote tmux detail: %q", resolved.RemoteTmuxDetail)
	}
}

func TestAttachTargetMarksRemoteTmuxFailureWithoutFailingAttach(t *testing.T) {
	controller := &fakeTmuxController{
		snapshots: []string{
			"[Host]>",
			"1 | root | Root Account\nID>",
			"资产[test4(10.0.0.4)]\nroot@test4:~$",
		},
		sendHook: func(text string) {},
	}
	controller.sendHook = func(text string) {
		marker := extractRemoteTmuxMarker(text)
		if marker == "" {
			return
		}
		controller.snapshots = append(controller.snapshots, marker+"\n"+marker+" failed attach-session exit=1\nroot@test4:~$")
	}
	client := newTestRemoteClient(t, controller)

	resolved, err := client.AttachTarget("%1", "2601")
	if err != nil {
		t.Fatalf("AttachTarget() error = %v", err)
	}
	if resolved.RemoteTmuxStatus != model.RemoteTmuxStatusFailed {
		t.Fatalf("expected remote tmux failed, got %q", resolved.RemoteTmuxStatus)
	}
	if !strings.Contains(resolved.RemoteTmuxDetail, "attach-session exit=1") {
		t.Fatalf("unexpected remote tmux detail: %q", resolved.RemoteTmuxDetail)
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

func TestResolveRunnerPathMaterializesBundledAssets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_GHOSTTY_HOME", dir)
	t.Setenv("TMUX_GHOSTTY_JUMP_RUNNER", "")

	runnerPath, err := resolveRunnerPath()
	if err != nil {
		t.Fatalf("resolveRunnerPath() error = %v", err)
	}
	if !strings.HasPrefix(runnerPath, filepath.Join(dir, bundledRunnerDir)) {
		t.Fatalf("expected runner to be materialized under runtime dir, got %q", runnerPath)
	}
	runnerInfo, err := os.Stat(runnerPath)
	if err != nil {
		t.Fatalf("stat runner: %v", err)
	}
	if runnerInfo.Mode()&0o111 == 0 {
		t.Fatalf("expected bundled runner to be executable, mode=%v", runnerInfo.Mode())
	}
	expectPath := filepath.Join(filepath.Dir(runnerPath), "jump_connect.exp")
	expectInfo, err := os.Stat(expectPath)
	if err != nil {
		t.Fatalf("stat expect helper: %v", err)
	}
	if expectInfo.Mode()&0o111 == 0 {
		t.Fatalf("expected bundled expect helper to be executable, mode=%v", expectInfo.Mode())
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
	existingHook := controller.sendHook
	controller.sendHook = func(text string) {
		before := len(controller.snapshots)
		if existingHook != nil {
			existingHook(text)
		}
		if len(controller.snapshots) != before {
			return
		}
		marker := extractRemoteTmuxMarker(text)
		if marker == "" {
			return
		}
		controller.snapshots = append(controller.snapshots, marker+"\nroot@test4:~$")
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

func extractRemoteTmuxMarker(command string) string {
	start := strings.Index(command, remoteTmuxMarkerPrefix)
	if start == -1 {
		return ""
	}
	value := command[start:]
	offset := strings.Index(value[len(remoteTmuxMarkerPrefix):], "__")
	if offset == -1 {
		return ""
	}
	return value[:len(remoteTmuxMarkerPrefix)+offset+2]
}
