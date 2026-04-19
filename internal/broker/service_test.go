package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/execx"
	"github.com/Woo-kk/tmux-ghostty/internal/ghostty"
	"github.com/Woo-kk/tmux-ghostty/internal/logx"
	"github.com/Woo-kk/tmux-ghostty/internal/model"
	"github.com/Woo-kk/tmux-ghostty/internal/remote"
	"github.com/Woo-kk/tmux-ghostty/internal/rpc"
	"github.com/Woo-kk/tmux-ghostty/internal/store"
	"github.com/Woo-kk/tmux-ghostty/internal/tmux"
)

type fakeGhosttyClient struct {
	mu              sync.Mutex
	windowCounter   int
	tabCounter      int
	terminalCounter int
	requireErr      error
	requireCalls    int
	ensureErr       error
	ensureCalls     int
	newWindowCalls  int
	windows         map[string]ghostty.WindowRef
	tabs            map[string][]ghostty.TabRef
	terminals       map[string][]ghostty.TerminalRef
}

type fakeRemoteClient struct {
	connectResult remote.ConnectedProvider
	connectErr    error
	attachResult  remote.ResolvedTarget
	attachErr     error
}

type trackingTmuxClient struct {
	base            *tmux.Client
	mu              sync.Mutex
	visibleSessions map[string]struct{}
	killCalls       []string
}

func newFakeGhosttyClient() *fakeGhosttyClient {
	return &fakeGhosttyClient{
		windows:   map[string]ghostty.WindowRef{},
		tabs:      map[string][]ghostty.TabRef{},
		terminals: map[string][]ghostty.TerminalRef{},
	}
}

func newTrackingTmuxClient(base *tmux.Client) *trackingTmuxClient {
	return &trackingTmuxClient{
		base:            base,
		visibleSessions: map[string]struct{}{},
	}
}

func (c *trackingTmuxClient) HasSession(name string) (bool, error) {
	c.mu.Lock()
	_, tracked := c.visibleSessions[name]
	c.mu.Unlock()
	if !tracked {
		return false, nil
	}
	return c.base.HasSession(name)
}

func (c *trackingTmuxClient) ListSessions() ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sessions := make([]string, 0, len(c.visibleSessions))
	for session := range c.visibleSessions {
		sessions = append(sessions, session)
	}
	sort.Strings(sessions)
	return sessions, nil
}

func (c *trackingTmuxClient) NewSession(name string) error {
	if err := c.base.NewSession(name); err != nil {
		return err
	}
	c.mu.Lock()
	c.visibleSessions[name] = struct{}{}
	c.mu.Unlock()
	return nil
}

func (c *trackingTmuxClient) KillSession(name string) error {
	err := c.base.KillSession(name)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killCalls = append(c.killCalls, name)
	delete(c.visibleSessions, name)
	return err
}

func (c *trackingTmuxClient) SendKeys(target string, text string) error {
	return c.base.SendKeys(target, text)
}

func (c *trackingTmuxClient) SendCtrlC(target string) error {
	return c.base.SendCtrlC(target)
}

func (c *trackingTmuxClient) CapturePane(target string, lines int) (string, error) {
	return c.base.CapturePane(target, lines)
}

func (c *trackingTmuxClient) CurrentCommand(target string) (string, error) {
	return c.base.CurrentCommand(target)
}

func (c *trackingTmuxClient) TargetAlive(target string) (bool, error) {
	return c.base.TargetAlive(target)
}

func (c *trackingTmuxClient) AttachCommand(session string) string {
	return c.base.AttachCommand(session)
}

func (f *fakeGhosttyClient) RequireAvailable() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requireCalls++
	return f.requireErr
}

func (f *fakeGhosttyClient) Available() error {
	return f.RequireAvailable()
}

func (f *fakeGhosttyClient) EnsureRunning() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureCalls++
	return f.ensureErr
}

func (f *fakeGhosttyClient) FocusTerminal(string) error {
	return nil
}

func (f *fakeGhosttyClient) NewWindow(string) (ghostty.WindowRef, ghostty.TerminalRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.newWindowCalls++
	f.windowCounter++
	f.tabCounter++
	f.terminalCounter++
	windowID := "window-test-" + itoa(f.windowCounter)
	tabID := "tab-test-" + itoa(f.tabCounter)
	terminalID := "term-test-" + itoa(f.terminalCounter)
	window := ghostty.WindowRef{ID: windowID, Name: windowID, SelectedTabID: tabID}
	tab := ghostty.TabRef{ID: tabID, Name: tabID, Index: 1, Selected: true, FocusedTerminalID: terminalID}
	terminal := ghostty.TerminalRef{ID: terminalID, Name: terminalID}
	f.windows[windowID] = window
	f.tabs[windowID] = []ghostty.TabRef{tab}
	f.terminals[tabID] = []ghostty.TerminalRef{terminal}
	return window, terminal, nil
}

func (f *fakeGhosttyClient) NewTab(windowID string, _ string) (ghostty.TabRef, ghostty.TerminalRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tabCounter++
	f.terminalCounter++
	tabID := "tab-test-" + itoa(f.tabCounter)
	terminalID := "term-test-" + itoa(f.terminalCounter)
	tab := ghostty.TabRef{ID: tabID, Name: tabID, Index: len(f.tabs[windowID]) + 1, Selected: true, FocusedTerminalID: terminalID}
	terminal := ghostty.TerminalRef{ID: terminalID, Name: terminalID}
	f.tabs[windowID] = append(f.tabs[windowID], tab)
	f.terminals[tabID] = []ghostty.TerminalRef{terminal}
	window := f.windows[windowID]
	window.SelectedTabID = tabID
	f.windows[windowID] = window
	return tab, terminal, nil
}

func (f *fakeGhosttyClient) ListWindows() ([]ghostty.WindowRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ghostty.WindowRef, 0, len(f.windows))
	for _, window := range f.windows {
		out = append(out, window)
	}
	return out, nil
}

func (f *fakeGhosttyClient) ListTabs(windowID string) ([]ghostty.TabRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ghostty.TabRef(nil), f.tabs[windowID]...), nil
}

func (f *fakeGhosttyClient) ListTerminals(tabID string) ([]ghostty.TerminalRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ghostty.TerminalRef(nil), f.terminals[tabID]...), nil
}

func (f fakeRemoteClient) SearchTarget(query string) ([]remote.TargetMatch, error) {
	return []remote.TargetMatch{{DisplayName: query}}, nil
}

func (f fakeRemoteClient) ConnectTarget(localTarget string) (remote.ConnectedProvider, error) {
	if f.connectErr != nil {
		return remote.ConnectedProvider{}, f.connectErr
	}
	result := f.connectResult
	if strings.TrimSpace(result.Provider) == "" {
		result.Provider = remote.ProviderJumpServer
	}
	if result.Stage == "" {
		result.Stage = model.StageMenu
	}
	if len(result.StageTrace) == 0 && result.Stage != "" {
		result.StageTrace = []model.PaneStage{result.Stage}
	}
	if !result.ReadyForUserInput {
		result.ReadyForUserInput = result.Stage == model.StageMenu || result.Stage == model.StageTargetSearch || result.Stage == model.StageAuthPrompt
	}
	return result, nil
}

func (f fakeRemoteClient) AttachTarget(localTarget string, query string) (remote.ResolvedTarget, error) {
	if f.attachErr != nil {
		return remote.ResolvedTarget{}, f.attachErr
	}
	result := f.attachResult
	if strings.TrimSpace(result.Query) == "" {
		result.Query = query
	}
	if strings.TrimSpace(result.Name) == "" {
		result.Name = query
	}
	if strings.TrimSpace(result.RemoteSession) == "" {
		result.RemoteSession = "tmux-ghostty"
	}
	if strings.TrimSpace(result.Provider) == "" {
		result.Provider = remote.ProviderJumpServer
	}
	if result.RemoteTmuxStatus == "" {
		result.RemoteTmuxStatus = model.RemoteTmuxStatusAttached
	}
	return result, nil
}

func (f fakeRemoteClient) EnsureRemoteSession(localTarget string, remoteSession string) error {
	return nil
}

func (f fakeRemoteClient) Reconnect(localTarget string) error { return nil }

func (f fakeRemoteClient) DetectStage(text string) model.PaneStage {
	return remote.DetectStage(text)
}

func TestShouldAutoExitLocked(t *testing.T) {
	service := newTestService(t)
	now := time.Now().UTC()

	service.state.LastRequestAt = now.Add(-service.idleTimeout).Add(-time.Second)
	if !service.shouldAutoExitLocked(now) {
		t.Fatalf("expected auto exit when idle with no active workspace or pane")
	}

	workspace := model.NewWorkspace()
	pane := model.NewPane(workspace.ID)
	workspace.PaneIDs = []string{pane.ID}
	service.state.Workspaces[workspace.ID] = workspace
	service.state.Panes[pane.ID] = pane
	if service.shouldAutoExitLocked(now) {
		t.Fatalf("did not expect auto exit with active workspace and pane")
	}
}

func TestCommandFlowWithTmux(t *testing.T) {
	service := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}

	created, err := service.CreateWorkspace()
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pane := created.Pane
	t.Cleanup(func() {
		_ = service.CloseWorkspace(created.Workspace.ID)
	})

	if _, err := service.Claim(pane.ID, "agent"); err != nil {
		t.Fatalf("claim agent: %v", err)
	}
	if _, err := service.SendCommand(pane.ID, "agent", "pwd", ""); err != nil {
		t.Fatalf("send pwd: %v", err)
	}
	if _, err := waitForSnapshot(t, service, pane.ID, cwd); err != nil {
		t.Fatalf("wait for pwd output: %v", err)
	}

	targetFile := filepath.Join(t.TempDir(), "approval-flow.txt")
	preview, err := service.PreviewCommand(pane.ID, "agent", "echo risky > "+targetFile)
	if err != nil {
		t.Fatalf("preview risky command: %v", err)
	}
	if !preview.RequiresApproval || preview.Action == nil {
		t.Fatalf("expected approval to be required")
	}
	if _, err := service.Approve(preview.Action.ID); err != nil {
		t.Fatalf("approve action: %v", err)
	}
	if err := waitForFile(targetFile, 5*time.Second); err != nil {
		t.Fatalf("wait for risky command side effect: %v", err)
	}

	sleepPreview, err := service.PreviewCommand(pane.ID, "agent", "sleep 30")
	if err != nil {
		t.Fatalf("preview sleep command: %v", err)
	}
	if _, err := service.Approve(sleepPreview.Action.ID); err != nil {
		t.Fatalf("approve sleep action: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	interrupted, err := service.InterruptPane(pane.ID)
	if err != nil {
		t.Fatalf("interrupt pane: %v", err)
	}
	if interrupted.Mode != model.ModeIdle {
		t.Fatalf("expected pane to become idle after interrupt, got %q", interrupted.Mode)
	}

	released, err := service.Release(pane.ID)
	if err != nil {
		t.Fatalf("release pane: %v", err)
	}
	if released.Controller != model.ControllerUser {
		t.Fatalf("expected controller user after release, got %q", released.Controller)
	}
}

func TestAttachHostPersistsRemoteTmuxMetadata(t *testing.T) {
	service := newTestServiceWithRemote(t, fakeRemoteClient{
		attachResult: remote.ResolvedTarget{
			RemoteSession:    "tmux-ghostty",
			Provider:         remote.ProviderJumpServer,
			RemoteTmuxStatus: model.RemoteTmuxStatusUnavailable,
			RemoteTmuxDetail: "tmux not found",
		},
	})
	created, err := service.CreateWorkspace()
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_ = service.CloseWorkspace(created.Workspace.ID)
	})

	result, err := service.AttachHost(created.Pane.ID, "2801")
	if err != nil {
		t.Fatalf("attach host: %v", err)
	}
	if result.Pane.RemoteTmuxStatus != model.RemoteTmuxStatusUnavailable {
		t.Fatalf("expected pane remote tmux status unavailable, got %q", result.Pane.RemoteTmuxStatus)
	}
	if result.Pane.RemoteTmuxDetail != "tmux not found" {
		t.Fatalf("unexpected pane remote tmux detail: %q", result.Pane.RemoteTmuxDetail)
	}
	if result.Target.RemoteTmuxStatus != model.RemoteTmuxStatusUnavailable {
		t.Fatalf("expected target remote tmux status unavailable, got %q", result.Target.RemoteTmuxStatus)
	}

	snapshot, err := service.SnapshotPane(created.Pane.ID)
	if err != nil {
		t.Fatalf("snapshot pane: %v", err)
	}
	if snapshot.RemoteTmuxStatus != model.RemoteTmuxStatusUnavailable {
		t.Fatalf("expected snapshot remote tmux status unavailable, got %q", snapshot.RemoteTmuxStatus)
	}
	if snapshot.RemoteTmuxDetail != "tmux not found" {
		t.Fatalf("unexpected snapshot remote tmux detail: %q", snapshot.RemoteTmuxDetail)
	}
}

func TestConnectHostStopsAtJumpServerMenu(t *testing.T) {
	service := newTestServiceWithRemote(t, fakeRemoteClient{
		connectResult: remote.ConnectedProvider{
			Provider:          remote.ProviderJumpServer,
			Stage:             model.StageTargetSearch,
			StageTrace:        []model.PaneStage{model.StageMenu, model.StageTargetSearch},
			ReadyForUserInput: true,
		},
	})
	created, err := service.CreateWorkspace()
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_ = service.CloseWorkspace(created.Workspace.ID)
	})

	result, err := service.ConnectHost(created.Pane.ID)
	if err != nil {
		t.Fatalf("connect host: %v", err)
	}
	if result.Provider != remote.ProviderJumpServer {
		t.Fatalf("unexpected provider: %q", result.Provider)
	}
	if result.Stage != model.StageTargetSearch {
		t.Fatalf("unexpected stage: %q", result.Stage)
	}
	if !result.ReadyForUserInput {
		t.Fatalf("expected connect result to be ready for user input")
	}
	if len(result.StageTrace) != 2 {
		t.Fatalf("unexpected stage trace: %#v", result.StageTrace)
	}
	if result.Pane.RemoteProvider != remote.ProviderJumpServer {
		t.Fatalf("expected pane remote provider jumpserver, got %q", result.Pane.RemoteProvider)
	}
	if result.Pane.HostQuery != "" || result.Pane.HostResolvedName != "" {
		t.Fatalf("expected host metadata to stay empty after connect, got %+v", result.Pane)
	}
}

func TestCloseWorkspacePrunesTrackedStateAndActions(t *testing.T) {
	service, tmuxClient := newTestServiceWithRemoteAndTmux(t, fakeRemoteClient{})
	created, err := service.CreateWorkspace()
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := service.Claim(created.Pane.ID, "agent"); err != nil {
		t.Fatalf("claim agent: %v", err)
	}
	preview, err := service.PreviewCommand(created.Pane.ID, "agent", "echo risky > "+filepath.Join(t.TempDir(), "close-workspace.txt"))
	if err != nil {
		t.Fatalf("preview risky command: %v", err)
	}
	if !preview.RequiresApproval || preview.Action == nil {
		t.Fatalf("expected pending action before close")
	}
	service.lastObserved[created.Pane.ID] = time.Now().UTC()

	if err := service.CloseWorkspace(created.Workspace.ID); err != nil {
		t.Fatalf("close workspace: %v", err)
	}

	if len(service.state.Workspaces) != 0 || len(service.state.Panes) != 0 {
		t.Fatalf("expected workspace and panes to be pruned, got workspaces=%d panes=%d", len(service.state.Workspaces), len(service.state.Panes))
	}
	if len(service.actions) != 0 {
		t.Fatalf("expected actions to be pruned, got %d", len(service.actions))
	}
	if _, ok := service.lastObserved[created.Pane.ID]; ok {
		t.Fatalf("expected observe state to be pruned for %s", created.Pane.ID)
	}
	alive, err := tmuxClient.HasSession(created.Pane.LocalTmuxSession)
	if err != nil {
		t.Fatalf("check tmux session: %v", err)
	}
	if alive {
		t.Fatalf("expected owned tmux session %s to be killed", created.Pane.LocalTmuxSession)
	}
	actions, err := service.ListActions()
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("expected actions.list to be empty after close, got %d", len(actions))
	}
}

func TestNewServiceStartupGCPrunesPersistedStaleState(t *testing.T) {
	dir := t.TempDir()
	logger, err := logx.New("")
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	runner := execx.NewRunner(logger)
	tmuxClient := newTrackingTmuxClient(tmux.New(runner))

	closedWorkspace := model.NewWorkspace()
	closedWorkspace.Status = model.WorkspaceClosed
	closedPane := model.NewPane(closedWorkspace.ID)
	closedPane.Mode = model.ModeDisconnected
	closedWorkspace.PaneIDs = []string{closedPane.ID}
	if err := tmuxClient.NewSession(closedPane.LocalTmuxSession); err != nil {
		t.Fatalf("seed closed tmux session: %v", err)
	}

	staleWorkspace := model.NewWorkspace()
	staleWorkspace.Status = model.WorkspaceDegraded
	staleWorkspace.GhosttyWindowID = "missing-window"
	staleWorkspace.GhosttyTabID = "missing-tab"
	stalePane := model.NewPane(staleWorkspace.ID)
	stalePane.GhosttyTerminalID = "missing-terminal"
	stalePane.Mode = model.ModeDisconnected
	staleWorkspace.PaneIDs = []string{stalePane.ID}
	if err := tmuxClient.NewSession(stalePane.LocalTmuxSession); err != nil {
		t.Fatalf("seed stale tmux session: %v", err)
	}

	state := model.NewState()
	state.Workspaces[closedWorkspace.ID] = closedWorkspace
	state.Workspaces[staleWorkspace.ID] = staleWorkspace
	state.Panes[closedPane.ID] = closedPane
	state.Panes[stalePane.ID] = stalePane
	actions := []model.Action{
		model.NewAction(closedPane.ID, "agent", "echo closed", "echo closed", model.RiskRisky, model.ApprovalPending, model.ActionQueued),
		model.NewAction(stalePane.ID, "agent", "echo stale", "echo stale", model.RiskRead, model.ApprovalNotRequired, model.ActionCompleted),
	}

	statePath := filepath.Join(dir, "state.json")
	actionsPath := filepath.Join(dir, "actions.json")
	stateStore := store.New(statePath, actionsPath)
	if err := stateStore.SaveState(state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := stateStore.SaveActions(actions); err != nil {
		t.Fatalf("save actions: %v", err)
	}

	service, err := NewService(
		statePath,
		actionsPath,
		2*time.Second,
		logger,
		newFakeGhosttyClient(),
		tmuxClient,
		fakeRemoteClient{},
	)
	if err != nil {
		t.Fatalf("create service: %v", err)
	}

	if len(service.state.Workspaces) != 0 || len(service.state.Panes) != 0 {
		t.Fatalf("expected startup GC to prune stale persisted state, got workspaces=%d panes=%d", len(service.state.Workspaces), len(service.state.Panes))
	}
	if len(service.actions) != 0 {
		t.Fatalf("expected startup GC to prune persisted actions, got %d", len(service.actions))
	}
	for _, session := range []string{closedPane.LocalTmuxSession, stalePane.LocalTmuxSession} {
		alive, err := tmuxClient.HasSession(session)
		if err != nil {
			t.Fatalf("check session %s: %v", session, err)
		}
		if alive {
			t.Fatalf("expected startup GC to kill session %s", session)
		}
	}
	persistedState, err := stateStore.LoadState()
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if len(persistedState.Workspaces) != 0 || len(persistedState.Panes) != 0 {
		t.Fatalf("expected persisted state to be compacted, got %#v", persistedState)
	}
	persistedActions, err := stateStore.LoadActions()
	if err != nil {
		t.Fatalf("reload actions: %v", err)
	}
	if len(persistedActions) != 0 {
		t.Fatalf("expected persisted actions to be compacted, got %d", len(persistedActions))
	}
}

func TestPeriodicGCRemovesOrphanManagedSessionsOnly(t *testing.T) {
	service, tmuxClient := newTestServiceWithRemoteAndTmux(t, fakeRemoteClient{})
	managedSession := model.NewPane("ws-orphan-test").LocalTmuxSession
	nonManagedSession := fmt.Sprintf("usergc-%d", time.Now().UnixNano())
	if err := tmuxClient.NewSession(managedSession); err != nil {
		t.Fatalf("seed orphan managed session: %v", err)
	}
	if err := tmuxClient.NewSession(nonManagedSession); err != nil {
		t.Fatalf("seed non-managed session: %v", err)
	}

	service.lastGCAt = time.Now().UTC().Add(-observeGCInterval - time.Second)
	service.pollOnce(time.Now().UTC())

	managedAlive, err := tmuxClient.HasSession(managedSession)
	if err != nil {
		t.Fatalf("check managed session: %v", err)
	}
	if managedAlive {
		t.Fatalf("expected periodic GC to kill orphan managed session")
	}
	nonManagedAlive, err := tmuxClient.HasSession(nonManagedSession)
	if err != nil {
		t.Fatalf("check non-managed session: %v", err)
	}
	if !nonManagedAlive {
		t.Fatalf("expected periodic GC to keep non-managed session")
	}
	_ = tmuxClient.KillSession(nonManagedSession)
}

func TestStatusGCPrunesStaleEntriesAndShrinksCounts(t *testing.T) {
	service := newTestService(t)
	healthy, err := service.CreateWorkspace()
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_ = service.CloseWorkspace(healthy.Workspace.ID)
	})

	staleWorkspace := model.NewWorkspace()
	staleWorkspace.LaunchMode = model.WorkspaceLaunchModeCurrentWindow
	staleWorkspace.GhosttyWindowID = "missing-window"
	staleWorkspace.GhosttyTabID = "missing-tab"
	stalePane := model.NewPane(staleWorkspace.ID)
	stalePane.GhosttyTerminalID = "missing-terminal"
	staleWorkspace.PaneIDs = []string{stalePane.ID}
	service.state.Workspaces[staleWorkspace.ID] = staleWorkspace
	service.state.Panes[stalePane.ID] = stalePane
	service.actions = append(service.actions, model.NewAction(stalePane.ID, "agent", "echo stale", "echo stale", model.RiskRead, model.ApprovalNotRequired, model.ActionCompleted))

	status, err := service.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.WorkspaceCount != 1 || status.PaneCount != 1 {
		t.Fatalf("expected stale entries to be pruned from status counts, got %+v", status)
	}
	if len(status.Workspaces) != 1 || status.Workspaces[0].ID != healthy.Workspace.ID {
		t.Fatalf("expected only healthy workspace in status, got %+v", status.Workspaces)
	}
	if len(status.Panes) != 1 || status.Panes[0].ID != healthy.Pane.ID {
		t.Fatalf("expected only healthy pane in status, got %+v", status.Panes)
	}
	if _, ok := service.state.Workspaces[staleWorkspace.ID]; ok {
		t.Fatalf("expected stale workspace to be pruned from broker state")
	}
	if _, ok := service.state.Panes[stalePane.ID]; ok {
		t.Fatalf("expected stale pane to be pruned from broker state")
	}
	if len(service.actions) != 0 {
		t.Fatalf("expected actions tied to pruned panes to be removed, got %d", len(service.actions))
	}
}

func TestGCPreservesHealthyWorkspaceAndSession(t *testing.T) {
	service, tmuxClient := newTestServiceWithRemoteAndTmux(t, fakeRemoteClient{})
	created, err := service.CreateWorkspace()
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_ = service.CloseWorkspace(created.Workspace.ID)
	})

	service.gcLocked(time.Now().UTC())

	if len(service.state.Workspaces) != 1 || len(service.state.Panes) != 1 {
		t.Fatalf("expected healthy workspace to survive GC, got workspaces=%d panes=%d", len(service.state.Workspaces), len(service.state.Panes))
	}
	alive, err := tmuxClient.HasSession(created.Pane.LocalTmuxSession)
	if err != nil {
		t.Fatalf("check tmux session: %v", err)
	}
	if !alive {
		t.Fatalf("expected healthy pane session to remain alive")
	}
}

func TestReconcileDoesNotImportUnmanagedCurrentWindow(t *testing.T) {
	service := newTestService(t)
	fakeGhostty := service.ghostty.(*fakeGhosttyClient)
	if _, _, err := fakeGhostty.NewWindow(""); err != nil {
		t.Fatalf("seed unmanaged window: %v", err)
	}

	workspaces, err := service.Reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("expected reconcile to ignore unmanaged Ghostty windows, got %d workspaces", len(workspaces))
	}
	if len(service.state.Workspaces) != 0 || len(service.state.Panes) != 0 {
		t.Fatalf("expected no imported state after reconcile")
	}
}

func TestReconcileDoesNotRebuildCurrentWindowWorkspace(t *testing.T) {
	service := newTestService(t)
	fakeGhostty := service.ghostty.(*fakeGhosttyClient)

	workspace := model.NewWorkspace()
	workspace.LaunchMode = model.WorkspaceLaunchModeCurrentWindow
	workspace.GhosttyWindowID = "missing-window"
	workspace.GhosttyTabID = "missing-tab"
	pane := model.NewPane(workspace.ID)
	pane.GhosttyTerminalID = "missing-terminal"
	workspace.PaneIDs = []string{pane.ID}
	service.state.Workspaces[workspace.ID] = workspace
	service.state.Panes[pane.ID] = pane

	workspaces, err := service.Reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("expected stale current-window workspace to be pruned, got %d workspaces", len(workspaces))
	}
	if len(service.state.Workspaces) != 0 || len(service.state.Panes) != 0 {
		t.Fatalf("expected stale current-window workspace state to be removed")
	}
	if fakeGhostty.ensureCalls != 0 {
		t.Fatalf("expected reconcile not to call EnsureRunning for current-window workspace, got %d", fakeGhostty.ensureCalls)
	}
	if fakeGhostty.newWindowCalls != 0 {
		t.Fatalf("expected reconcile not to create a new window for current-window workspace, got %d", fakeGhostty.newWindowCalls)
	}
}

func TestReconcileDoesNotRebuildMultiPaneWorkspace(t *testing.T) {
	service := newTestService(t)
	fakeGhostty := service.ghostty.(*fakeGhosttyClient)

	workspace := model.NewWorkspace()
	workspace.GhosttyWindowID = "missing-window"
	workspace.GhosttyTabID = "missing-tab"
	firstPane := model.NewPane(workspace.ID)
	firstPane.GhosttyTerminalID = "missing-terminal-1"
	secondPane := model.NewPane(workspace.ID)
	secondPane.GhosttyTerminalID = "missing-terminal-2"
	workspace.PaneIDs = []string{firstPane.ID, secondPane.ID}
	service.state.Workspaces[workspace.ID] = workspace
	service.state.Panes[firstPane.ID] = firstPane
	service.state.Panes[secondPane.ID] = secondPane

	workspaces, err := service.Reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("expected stale multi-pane workspace to be pruned, got %d workspaces", len(workspaces))
	}
	if len(service.state.Workspaces) != 0 || len(service.state.Panes) != 0 {
		t.Fatalf("expected stale multi-pane workspace state to be removed")
	}
	if fakeGhostty.ensureCalls != 0 {
		t.Fatalf("expected reconcile not to call EnsureRunning for multi-pane workspace, got %d", fakeGhostty.ensureCalls)
	}
	if fakeGhostty.newWindowCalls != 0 {
		t.Fatalf("expected reconcile not to create a new window for multi-pane workspace, got %d", fakeGhostty.newWindowCalls)
	}
}

func TestStatusSyncClearsMissingCurrentWindowTopology(t *testing.T) {
	service := newTestService(t)
	fakeGhostty := service.ghostty.(*fakeGhosttyClient)

	workspace := model.NewWorkspace()
	workspace.LaunchMode = model.WorkspaceLaunchModeCurrentWindow
	workspace.GhosttyWindowID = "missing-window"
	workspace.GhosttyTabID = "missing-tab"
	pane := model.NewPane(workspace.ID)
	pane.GhosttyTerminalID = "missing-terminal"
	workspace.PaneIDs = []string{pane.ID}
	service.state.Workspaces[workspace.ID] = workspace
	service.state.Panes[pane.ID] = pane

	status, err := service.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.WorkspaceCount != 0 || status.PaneCount != 0 {
		t.Fatalf("unexpected status counts: %+v", status)
	}
	if len(service.state.Workspaces) != 0 || len(service.state.Panes) != 0 {
		t.Fatalf("expected stale current-window topology to be pruned after status sync")
	}
	if fakeGhostty.ensureCalls != 0 {
		t.Fatalf("expected status sync not to call EnsureRunning, got %d", fakeGhostty.ensureCalls)
	}
	if fakeGhostty.newWindowCalls != 0 {
		t.Fatalf("expected status sync not to create a new window, got %d", fakeGhostty.newWindowCalls)
	}
}

func TestRPCServerRoundTrip(t *testing.T) {
	service := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.SetShutdownFunc(cancel)
	service.Start(ctx)

	socketPath := filepath.Join(t.TempDir(), "broker.sock")
	server := rpc.Server{
		SocketPath: socketPath,
		Handler:    service.HandleRPC,
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Listen(ctx)
	}()
	waitForSocket(t, socketPath)

	client := rpc.NewClient(socketPath)
	var created WorkspaceCreateResult
	if err := client.Call(ctx, "workspace.create", nil, &created); err != nil {
		t.Fatalf("rpc workspace.create: %v", err)
	}

	var panes []model.Pane
	if err := client.Call(ctx, "pane.list", nil, &panes); err != nil {
		t.Fatalf("rpc pane.list: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("expected one pane after workspace.create, got %d", len(panes))
	}

	if err := client.Call(ctx, "broker.shutdown", map[string]any{"force": true}, &struct{}{}); err != nil {
		t.Fatalf("rpc broker.shutdown: %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for rpc server to stop")
	}
	_ = service.CloseWorkspace(created.Workspace.ID)
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	service, _ := newTestServiceWithRemoteAndTmux(t, fakeRemoteClient{})
	return service
}

func newTestServiceWithRemote(t *testing.T, remoteClient RemoteClient) *Service {
	t.Helper()
	service, _ := newTestServiceWithRemoteAndTmux(t, remoteClient)
	return service
}

func newTestServiceWithRemoteAndTmux(t *testing.T, remoteClient RemoteClient) (*Service, *trackingTmuxClient) {
	t.Helper()
	dir := t.TempDir()
	logger, err := logx.New("")
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	runner := execx.NewRunner(logger)
	tmuxClient := newTrackingTmuxClient(tmux.New(runner))
	service, err := NewService(
		filepath.Join(dir, "state.json"),
		filepath.Join(dir, "actions.json"),
		2*time.Second,
		logger,
		newFakeGhosttyClient(),
		tmuxClient,
		remoteClient,
	)
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	return service, tmuxClient
}

func waitForSnapshot(t *testing.T, service *Service, paneID string, substring string) (model.PaneSnapshot, error) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := service.SnapshotPane(paneID)
		if err != nil {
			return model.PaneSnapshot{}, err
		}
		if strings.Contains(snapshot.Text, substring) {
			return snapshot, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return model.PaneSnapshot{}, context.DeadlineExceeded
}

func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return context.DeadlineExceeded
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("socket did not appear: %s", path)
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}
