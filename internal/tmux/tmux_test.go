package tmux

import (
	"fmt"
	"testing"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/execx"
	"github.com/Woo-kk/tmux-ghostty/internal/logx"
)

func TestTargetAlive(t *testing.T) {
	logger, err := logx.New("")
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	client := New(execx.NewRunner(logger))

	session := fmt.Sprintf("tmux-ghostty-test-%d", time.Now().UnixNano())
	if err := client.NewSession(session); err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() {
		_ = client.KillSession(session)
	})

	alive, err := client.TargetAlive(session + ":0.0")
	if err != nil {
		t.Fatalf("check live target: %v", err)
	}
	if !alive {
		t.Fatalf("expected live target %s to be reported alive", session)
	}

	alive, err = client.TargetAlive("missing-session:0.0")
	if err != nil {
		t.Fatalf("check missing target: %v", err)
	}
	if alive {
		t.Fatalf("expected missing target to be reported dead")
	}
}
