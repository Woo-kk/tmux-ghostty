package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/logx"
)

type Result struct {
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration"`
}

type Runner struct {
	Log *logx.Logger
}

func NewRunner(log *logx.Logger) *Runner {
	return &Runner{Log: log}
}

func (r *Runner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (Result, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if r.Log != nil {
		r.Log.Info("exec.start", map[string]any{
			"cmd":  name,
			"args": args,
		})
	}

	err := cmd.Run()
	result := Result{
		Stdout:   strings.TrimRight(stdout.String(), "\n"),
		Stderr:   strings.TrimRight(stderr.String(), "\n"),
		ExitCode: exitCode(err),
		Duration: time.Since(start),
	}

	if r.Log != nil {
		r.Log.Info("exec.finish", map[string]any{
			"cmd":       name,
			"args":      args,
			"exit_code": result.ExitCode,
			"stdout":    truncate(result.Stdout),
			"stderr":    truncate(result.Stderr),
			"duration":  result.Duration.String(),
		})
	}

	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return result, fmt.Errorf("command timed out: %s: %w", name, ctx.Err())
		}
		return result, fmt.Errorf("command failed: %s %s: %w", name, strings.Join(args, " "), err)
	}
	return result, nil
}

func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n'\"\\$`!&|;<>*?()[]{}") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
		return exitErr.ExitCode()
	}
	return -1
}

func truncate(value string) string {
	const maxLen = 2000
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "...(truncated)"
}
