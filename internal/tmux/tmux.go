package tmux

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/guyuanshun/tmux-ghostty/internal/execx"
)

const (
	defaultTimeout = 10 * time.Second
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
	_, err := c.run(defaultTimeout, "new-session", "-d", "-A", "-s", name)
	return err
}

func (c *Client) KillSession(name string) error {
	_, err := c.run(defaultTimeout, "kill-session", "-t", name)
	if err != nil && strings.Contains(err.Error(), "can't find session") {
		return nil
	}
	return err
}

func (c *Client) SendKeys(session string, text string) error {
	target := paneTarget(session)
	if text != "" {
		if _, err := c.run(defaultTimeout, "send-keys", "-t", target, "-l", text); err != nil {
			return err
		}
	}
	_, err := c.run(defaultTimeout, "send-keys", "-t", target, "Enter")
	return err
}

func (c *Client) SendCtrlC(session string) error {
	_, err := c.run(defaultTimeout, "send-keys", "-t", paneTarget(session), "C-c")
	return err
}

func (c *Client) CapturePane(session string, lines int) (string, error) {
	if lines <= 0 {
		lines = 500
	}
	result, err := c.run(defaultTimeout, "capture-pane", "-p", "-J", "-t", paneTarget(session), "-S", fmt.Sprintf("-%d", lines))
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (c *Client) CurrentCommand(session string) (string, error) {
	result, err := c.run(defaultTimeout, "display-message", "-p", "-t", paneTarget(session), "#{pane_current_command}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (c *Client) SessionAlive(session string) (bool, error) {
	return c.HasSession(session)
}

func (c *Client) AttachCommand(session string) string {
	return "exec tmux attach-session -t " + execx.ShellQuote(session)
}

func (c *Client) run(timeout time.Duration, args ...string) (execx.Result, error) {
	return c.runner.Run(context.Background(), timeout, "tmux", args...)
}

func paneTarget(session string) string {
	return session + ":0.0"
}
