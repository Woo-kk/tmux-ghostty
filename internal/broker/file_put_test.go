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

// recordingTmux captures all SendKeys / SendBuffer calls and otherwise
// answers benignly.
type recordingTmux struct {
	mu         sync.Mutex
	sendKeys   []sendKeysCall
	sendBuffer []sendBufferCall
}

type sendKeysCall struct {
	target string
	text   string
}

type sendBufferCall struct {
	target     string
	bufferName string
	data       []byte
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
func (r *recordingTmux) SendBuffer(target, bufferName string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Copy because callers may reuse the slice.
	payload := make([]byte, len(data))
	copy(payload, data)
	r.sendBuffer = append(r.sendBuffer, sendBufferCall{target: target, bufferName: bufferName, data: payload})
	return nil
}
func (r *recordingTmux) SendCtrlC(string) error                  { return nil }
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

	keyCalls := tmuxRecorder.sendKeys
	if len(keyCalls) < 3 {
		t.Fatalf("expected at least 3 SendKeys calls (heredoc open, close tag, finish cmd), got %d", len(keyCalls))
	}
	bufCalls := tmuxRecorder.sendBuffer
	if len(bufCalls) != 1 {
		t.Fatalf("expected exactly 1 SendBuffer call, got %d", len(bufCalls))
	}

	// First SendKeys must open the heredoc with the same tag that closes it.
	openCmd := keyCalls[0].text
	if !strings.Contains(openCmd, "cat > ") || !strings.Contains(openCmd, "<< '"+"TG_EOF_") {
		t.Fatalf("open heredoc malformed: %q", openCmd)
	}

	// The buffer payload should be base64(payload) followed by a trailing
	// newline so the last line inside the heredoc is terminated.
	wantBase64 := base64.StdEncoding.EncodeToString(payload) + "\n"
	if string(bufCalls[0].data) != wantBase64 {
		t.Fatalf("buffer payload mismatch.\n got: %q\nwant: %q", bufCalls[0].data, wantBase64)
	}
	if !strings.HasPrefix(bufCalls[0].bufferName, "tg-file-put-") {
		t.Fatalf("buffer name should be namespaced per transfer, got %q", bufCalls[0].bufferName)
	}

	// Second SendKeys should be the close tag.
	closeCmd := keyCalls[1].text
	if !strings.HasPrefix(closeCmd, "TG_EOF_") || strings.Contains(closeCmd, " ") {
		t.Fatalf("close tag malformed: %q", closeCmd)
	}

	// Third SendKeys should be the finish command that decodes and emits the
	// done marker.
	finishCmd := keyCalls[2].text
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
