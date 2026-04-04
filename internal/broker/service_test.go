package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/execx"
	"github.com/Woo-kk/tmux-ghostty/internal/ghostty"
	"github.com/Woo-kk/tmux-ghostty/internal/jump"
	"github.com/Woo-kk/tmux-ghostty/internal/logx"
	"github.com/Woo-kk/tmux-ghostty/internal/model"
	"github.com/Woo-kk/tmux-ghostty/internal/rpc"
	"github.com/Woo-kk/tmux-ghostty/internal/tmux"
)

type fakeGhosttyClient struct {
	mu              sync.Mutex
	windowCounter   int
	tabCounter      int
	terminalCounter int
	windows         map[string]ghostty.WindowRef
	tabs            map[string][]ghostty.TabRef
	terminals       map[string][]ghostty.TerminalRef
}

type fakeJumpClient struct{}

func newFakeGhosttyClient() *fakeGhosttyClient {
	return &fakeGhosttyClient{
		windows:   map[string]ghostty.WindowRef{},
		tabs:      map[string][]ghostty.TabRef{},
		terminals: map[string][]ghostty.TerminalRef{},
	}
}

func (f *fakeGhosttyClient) Available() error                       { return nil }
func (f *fakeGhosttyClient) EnsureRunning() error                   { return nil }
func (f *fakeGhosttyClient) FocusTerminal(string) error             { return nil }
func (f *fakeGhosttyClient) InputText(string, string) error         { return nil }
func (f *fakeGhosttyClient) SendKey(string, string, []string) error { return nil }

func (f *fakeGhosttyClient) NewWindow(string) (ghostty.WindowRef, ghostty.TerminalRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeGhosttyClient) SplitTerminal(terminalID string, _ string, _ string) (ghostty.TerminalRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminalCounter++
	newTerminal := ghostty.TerminalRef{ID: "term-test-" + itoa(f.terminalCounter), Name: terminalID}
	for tabID, terminals := range f.terminals {
		for _, existing := range terminals {
			if existing.ID == terminalID {
				f.terminals[tabID] = append(f.terminals[tabID], newTerminal)
				return newTerminal, nil
			}
		}
	}
	return newTerminal, nil
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

func (f fakeJumpClient) SearchHost(query string) ([]jump.HostMatch, error) {
	return []jump.HostMatch{{DisplayName: query}}, nil
}

func (f fakeJumpClient) AttachHost(localSession string, hostQuery string) (jump.ResolvedHost, error) {
	return jump.ResolvedHost{Query: hostQuery, Name: hostQuery, RemoteSession: "tmux-ghostty"}, nil
}

func (f fakeJumpClient) EnsureRemoteTmux(localSession string, remoteSession string) error { return nil }
func (f fakeJumpClient) Reconnect(localSession string) error                              { return nil }

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
	dir := t.TempDir()
	logger, err := logx.New("")
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	runner := execx.NewRunner(logger)
	tmuxClient := tmux.New(runner)
	service, err := NewService(
		filepath.Join(dir, "state.json"),
		filepath.Join(dir, "actions.json"),
		2*time.Second,
		logger,
		newFakeGhosttyClient(),
		tmuxClient,
		fakeJumpClient{},
	)
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	return service
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
