package ghostty

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Woo-kk/tmux-ghostty/internal/execx"
)

func TestRequireAvailableDoesNotLaunchGhostty(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "calls.log")
	writeExecutable(t, filepath.Join(binDir, "osascript"), `#!/bin/sh
echo osascript >> "$LOG_FILE"
exit 1
`)
	writeExecutable(t, filepath.Join(binDir, "open"), `#!/bin/sh
echo "open $*" >> "$LOG_FILE"
exit 0
`)
	t.Setenv("LOG_FILE", logPath)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	client := New(execx.NewRunner(nil))
	if err := client.RequireAvailable(); err == nil {
		t.Fatalf("expected RequireAvailable to fail when Ghostty is unavailable")
	}

	lines := readLogLines(t, logPath)
	if len(lines) != 1 || lines[0] != "osascript" {
		t.Fatalf("expected only osascript check without launch, got %v", lines)
	}
}

func TestEnsureRunningLaunchesGhosttyWhenUnavailable(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "calls.log")
	stateDir := filepath.Join(binDir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "osascript"), `#!/bin/sh
echo osascript >> "$LOG_FILE"
if [ -f "$STATE_DIR/running" ]; then
  echo "1.0"
  exit 0
fi
exit 1
`)
	writeExecutable(t, filepath.Join(binDir, "open"), `#!/bin/sh
echo "open $*" >> "$LOG_FILE"
touch "$STATE_DIR/running"
exit 0
`)
	t.Setenv("LOG_FILE", logPath)
	t.Setenv("STATE_DIR", stateDir)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	client := New(execx.NewRunner(nil))
	if err := client.EnsureRunning(); err != nil {
		t.Fatalf("EnsureRunning() error = %v", err)
	}

	lines := readLogLines(t, logPath)
	if len(lines) < 3 {
		t.Fatalf("expected availability check, launch, and re-check, got %v", lines)
	}
	if lines[0] != "osascript" {
		t.Fatalf("expected first call to be availability probe, got %v", lines)
	}
	if !strings.HasPrefix(lines[1], "open ") {
		t.Fatalf("expected second call to launch Ghostty, got %v", lines)
	}
	if lines[2] != "osascript" {
		t.Fatalf("expected EnsureRunning to re-check availability after launch, got %v", lines)
	}
}

func TestEnsureRunningSkipsLaunchWhenAlreadyAvailable(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "calls.log")
	stateDir := filepath.Join(binDir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "running"), []byte("1"), 0o644); err != nil {
		t.Fatalf("write running marker: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "osascript"), `#!/bin/sh
echo osascript >> "$LOG_FILE"
echo "1.0"
exit 0
`)
	writeExecutable(t, filepath.Join(binDir, "open"), `#!/bin/sh
echo "open $*" >> "$LOG_FILE"
exit 0
`)
	t.Setenv("LOG_FILE", logPath)
	t.Setenv("STATE_DIR", stateDir)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	client := New(execx.NewRunner(nil))
	if err := client.EnsureRunning(); err != nil {
		t.Fatalf("EnsureRunning() error = %v", err)
	}

	lines := readLogLines(t, logPath)
	if len(lines) != 1 || lines[0] != "osascript" {
		t.Fatalf("expected already-available path to skip launch, got %v", lines)
	}
}

func writeExecutable(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

func readLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log %s: %v", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
