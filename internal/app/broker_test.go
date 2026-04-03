package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupStaleRuntime(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{
		BaseDir:     dir,
		SocketPath:  filepath.Join(dir, "broker.sock"),
		PIDPath:     filepath.Join(dir, "broker.pid"),
		StatePath:   filepath.Join(dir, "state.json"),
		ActionsPath: filepath.Join(dir, "actions.json"),
		LogPath:     filepath.Join(dir, "broker.log"),
	}

	if err := os.WriteFile(paths.SocketPath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale socket placeholder: %v", err)
	}
	if err := os.WriteFile(paths.PIDPath, []byte("999999\n"), 0o644); err != nil {
		t.Fatalf("write stale pid: %v", err)
	}

	if err := cleanupStaleRuntime(paths); err != nil {
		t.Fatalf("cleanup stale runtime: %v", err)
	}

	if _, err := os.Stat(paths.SocketPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale socket placeholder to be removed, got err=%v", err)
	}
	if _, err := os.Stat(paths.PIDPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale pid file to be removed, got err=%v", err)
	}
}
