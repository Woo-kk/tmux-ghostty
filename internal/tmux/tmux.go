package tmux

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/execx"
)

const (
	defaultTimeout = 10 * time.Second
	// bufferTimeout is used for load-buffer/paste-buffer, which can block the
	// client briefly while the buffer drains into the pane's PTY. For very
	// large uploads (100 MB+) the paste can take a while, so we allow a much
	// longer timeout than the default tmux operations.
	bufferTimeout = 10 * time.Minute
)

type Client struct {
	runner *execx.Runner
}

func New(runner *execx.Runner) *Client {
	return &Client{runner: runner}
}

func (c *Client) HasSession(name string) (bool, error) {
	_, err := c.run(defaultTimeout, "has-session", "-t", name)
	if err != nil {
		if strings.Contains(err.Error(), "can't find session") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *Client) NewSession(name string) error {
	if _, err := c.run(defaultTimeout, "new-session", "-d", "-A", "-s", name); err != nil {
		return err
	}
	// Pin window/pane numbering to 0 for this session so the hardcoded ":0.0"
	// target works even when the user's global tmux config sets base-index or
	// pane-base-index to 1. set-option -w applies pane-base-index to every
	// window in the session; move-window -r renumbers the already-created
	// window so the initial index matches the updated base-index.
	if _, err := c.run(defaultTimeout, "set-option", "-t", name, "base-index", "0"); err != nil {
		return err
	}
	if _, err := c.run(defaultTimeout, "set-option", "-w", "-t", name, "pane-base-index", "0"); err != nil {
		return err
	}
	if _, err := c.run(defaultTimeout, "move-window", "-r", "-t", name); err != nil {
		return err
	}
	return nil
}

func (c *Client) KillSession(name string) error {
	_, err := c.run(defaultTimeout, "kill-session", "-t", name)
	if err != nil && strings.Contains(err.Error(), "can't find session") {
		return nil
	}
	return err
}

func (c *Client) SendKeys(target string, text string) error {
	target = normalizeTarget(target)
	if text != "" {
		if err := c.SendText(target, text); err != nil {
			return err
		}
	}
	_, err := c.run(defaultTimeout, "send-keys", "-t", target, "Enter")
	return err
}

func (c *Client) SendText(target string, text string) error {
	target = normalizeTarget(target)
	if text == "" {
		return nil
	}
	_, err := c.run(defaultTimeout, "send-keys", "-t", target, "-l", text)
	return err
}

// SendBuffer pastes arbitrary bytes into the pane via `tmux load-buffer` +
// `tmux paste-buffer`. Unlike `SendText` (which uses `send-keys -l`), this
// path is not subject to the ~16 KiB limit tmux imposes on send-keys
// arguments, so it is the right primitive for streaming large payloads.
//
// The data is loaded into a uniquely-named buffer (so concurrent transfers
// do not clobber each other) and then pasted into the target pane. `-d`
// deletes the buffer after paste. SendBuffer does not append a trailing
// Enter; the caller is responsible for sending one if the payload needs to
// be terminated.
func (c *Client) SendBuffer(target string, bufferName string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	target = normalizeTarget(target)
	if _, err := c.runStdin(bufferTimeout, data, "load-buffer", "-b", bufferName, "-"); err != nil {
		return err
	}
	_, err := c.run(bufferTimeout, "paste-buffer", "-b", bufferName, "-d", "-t", target)
	return err
}

func (c *Client) SendCtrlC(target string) error {
	_, err := c.run(defaultTimeout, "send-keys", "-t", normalizeTarget(target), "C-c")
	return err
}

func (c *Client) CapturePane(target string, lines int) (string, error) {
	if lines <= 0 {
		lines = 500
	}
	result, err := c.run(defaultTimeout, "capture-pane", "-p", "-J", "-t", normalizeTarget(target), "-S", fmt.Sprintf("-%d", lines))
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (c *Client) CurrentCommand(target string) (string, error) {
	result, err := c.run(defaultTimeout, "display-message", "-p", "-t", normalizeTarget(target), "#{pane_current_command}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (c *Client) TargetAlive(target string) (bool, error) {
	_, err := c.run(defaultTimeout, "display-message", "-p", "-t", normalizeTarget(target), "#{pane_id}")
	if err != nil {
		if strings.Contains(err.Error(), "can't find pane") || strings.Contains(err.Error(), "can't find window") {
			return false, nil
		}
		if strings.Contains(err.Error(), "can't find session") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *Client) AttachCommand(session string) string {
	return "exec tmux attach-session -t " + execx.ShellQuote(session)
}

func (c *Client) run(timeout time.Duration, args ...string) (execx.Result, error) {
	return c.runner.Run(context.Background(), timeout, "tmux", args...)
}

func (c *Client) runStdin(timeout time.Duration, stdin []byte, args ...string) (execx.Result, error) {
	return c.runner.RunStdin(context.Background(), timeout, stdin, "tmux", args...)
}

func normalizeTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if strings.Contains(target, ":") || strings.HasPrefix(target, "%") {
		return target
	}
	return target + ":0.0"
}
