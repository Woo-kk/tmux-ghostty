package broker

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/logx"
	"github.com/Woo-kk/tmux-ghostty/internal/model"
)

// recordingTmux captures all SendKeys calls and otherwise answers benignly.
type recordingTmux struct {
	mu       sync.Mutex
	sendKeys []sendKeysCall
}

type sendKeysCall struct {
	target string
	text   string
}

func (r *recordingTmux) HasSession(string) (bool, error) { return true, nil }
func (r *recordingTmux) NewSession(string) error         { return nil }
func (r *recordingTmux) KillSession(string) error        { return nil }
func (r *recordingTmux) SendKeys(target, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sendKeys = append(r.sendKeys, sendKeysCall{target: target, text: text})
	return nil
}
func (r *recordingTmux) SendCtrlC(string) error             { return nil }
func (r *recordingTmux) CapturePane(string, int) (string, error) { return "", nil }
func (r *recordingTmux) CurrentCommand(string) (string, error)   { return "", nil }
func (r *recordingTmux) TargetAlive(string) (bool, error)        { return true, nil }
func (r *recordingTmux) AttachCommand(s string) string           { return "tmux attach -t " + s }

func newServiceWithTmux(t *testing.T, tc TmuxClient) *Service {
	t.Helper()
	dir := t.TempDir()
	logger, err := logx.New("")
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	svc, err := NewService(
		filepath.Join(dir, "state.json"),
		filepath.Join(dir, "actions.json"),
		5*time.Second,
		logger,
		newFakeGhosttyClient(),
		tc,
		fakeRemoteClient{},
	)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return svc
}

// seedAgentControlledPane injects a pane directly into the service state so we
// don't depend on the full workspace creation path (which requires real tmux).
func seedAgentControlledPane(t *testing.T, svc *Service) model.Pane {
	t.Helper()
	svc.mu.Lock()
	defer svc.mu.Unlock()

	workspace := model.NewWorkspace()
	svc.state.Workspaces[workspace.ID] = workspace

	pane := model.NewPane(workspace.ID)
	pane.LocalTmuxSession = "tg-test-session"
	pane.LocalTmuxTarget = "tg-test-session:0.0"
	pane.Controller = model.ControllerAgent
	pane.Mode = model.ModeIdle
	pane.Stage = model.StageRemoteShell
	workspace.PaneIDs = append(workspace.PaneIDs, pane.ID)
	svc.state.Workspaces[workspace.ID] = workspace
	svc.state.Panes[pane.ID] = pane

	if err := svc.store.SaveState(svc.state); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	return pane
}

func TestPutFileStreamsBase64AndEmitsMarker(t *testing.T) {
	tmuxRecorder := &recordingTmux{}
	svc := newServiceWithTmux(t, tmuxRecorder)
	pane := seedAgentControlledPane(t, svc)

	payload := []byte("hello traffic-analytics\n")
	localDir := t.TempDir()
	localPath := filepath.Join(localDir, "payload.bin")
	if err := os.WriteFile(localPath, payload, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	result, err := svc.PutFile(pane.ID, localPath, "/opt/flink-job/traffic-analytics/out.bin")
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if result.Bytes != int64(len(payload)) {
		t.Fatalf("bytes: got %d want %d", result.Bytes, len(payload))
	}
	if !strings.HasPrefix(result.DoneMarker, "TG_PUT_DONE_") {
		t.Fatalf("done marker: got %q", result.DoneMarker)
	}
	if !strings.HasPrefix(result.TempPath, "/tmp/tg-upload-") {
		t.Fatalf("temp path: got %q", result.TempPath)
	}

	calls := tmuxRecorder.sendKeys
	if len(calls) < 4 {
		t.Fatalf("expected at least 4 SendKeys calls (heredoc open, chunk, heredoc close, finish), got %d", len(calls))
	}

	// First call must open the heredoc with the same tag that closes it.
	openCmd := calls[0].text
	if !strings.Contains(openCmd, "cat > ") || !strings.Contains(openCmd, "<< '"+"TG_EOF_") {
		t.Fatalf("open heredoc malformed: %q", openCmd)
	}

	// Reassemble all chunks between the opening heredoc command and the
	// closing tag. They should concatenate to the base64 of the payload.
	// Identify the closing tag by finding a line exactly matching the TG_EOF_*.
	var chunks []string
	closeIdx := -1
	for i := 1; i < len(calls); i++ {
		if strings.HasPrefix(calls[i].text, "TG_EOF_") && !strings.Contains(calls[i].text, " ") {
			closeIdx = i
			break
		}
		chunks = append(chunks, calls[i].text)
	}
	if closeIdx < 0 {
		t.Fatalf("did not find heredoc close tag in calls: %+v", calls)
	}
	gotBase64 := strings.Join(chunks, "")
	wantBase64 := base64.StdEncoding.EncodeToString(payload)
	if gotBase64 != wantBase64 {
		t.Fatalf("streamed base64 mismatch.\n got: %q\nwant: %q", gotBase64, wantBase64)
	}

	finishCmd := calls[closeIdx+1].text
	if !strings.Contains(finishCmd, "base64 -d < ") || !strings.Contains(finishCmd, result.DoneMarker) {
		t.Fatalf("finish command malformed: %q", finishCmd)
	}
}

func TestPutFileRequiresAgentControl(t *testing.T) {
	svc := newServiceWithTmux(t, &recordingTmux{})

	// Seed a pane where controller is the user.
	svc.mu.Lock()
	workspace := model.NewWorkspace()
	svc.state.Workspaces[workspace.ID] = workspace
	pane := model.NewPane(workspace.ID)
	pane.LocalTmuxSession = "tg-test"
	pane.LocalTmuxTarget = "tg-test:0.0"
	pane.Controller = model.ControllerUser
	pane.Mode = model.ModeIdle
	pane.Stage = model.StageRemoteShell
	svc.state.Panes[pane.ID] = pane
	svc.mu.Unlock()

	localDir := t.TempDir()
	localPath := filepath.Join(localDir, "payload.bin")
	if err := os.WriteFile(localPath, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := svc.PutFile(pane.ID, localPath, "/tmp/out.bin")
	if err == nil {
		t.Fatalf("expected error when pane is not controlled by agent")
	}
	var rpcErr interface{ GetReason() string }
	if errors.As(err, &rpcErr) {
		if rpcErr.GetReason() == "" {
			t.Fatalf("expected reason, got empty: %v", err)
		}
	}
}

func TestPutFileRejectsMissingLocalFile(t *testing.T) {
	svc := newServiceWithTmux(t, &recordingTmux{})
	pane := seedAgentControlledPane(t, svc)

	_, err := svc.PutFile(pane.ID, "/nonexistent/path/abc.bin", "/tmp/out.bin")
	if err == nil {
		t.Fatalf("expected error for missing local file")
	}
	if !strings.Contains(err.Error(), "read local file") {
		t.Fatalf("unexpected error: %v", err)
	}
}
