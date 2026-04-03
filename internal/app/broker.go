package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/guyuanshun/tmux-ghostty/internal/rpc"
)

func EnsureBroker(ctx context.Context, paths Paths) (*rpc.Client, error) {
	if err := paths.EnsureBaseDir(); err != nil {
		return nil, err
	}
	if err := cleanupStaleRuntime(paths); err != nil {
		return nil, err
	}

	client := rpc.NewClient(paths.SocketPath)
	if err := client.Call(ctx, "broker.status", nil, &struct{}{}); err == nil {
		return client, nil
	}

	if err := startBroker(paths); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.Call(ctx, "broker.status", nil, &struct{}{}); err == nil {
			return client, nil
		} else {
			lastErr = err
		}
		time.Sleep(150 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("broker did not become ready")
	}
	return nil, lastErr
}

func ConnectBroker(paths Paths) *rpc.Client {
	return rpc.NewClient(paths.SocketPath)
}

func WritePID(paths Paths, pid int) error {
	return os.WriteFile(paths.PIDPath, []byte(fmt.Sprintf("%d\n", pid)), 0o644)
}

func RemoveRuntimeFiles(paths Paths) {
	_ = os.Remove(paths.PIDPath)
	_ = os.Remove(paths.SocketPath)
}

func BrokerCommand(paths Paths) (string, []string, error) {
	if paths.BrokerBinary != "" {
		return paths.BrokerBinary, nil, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	sibling := filepath.Join(filepath.Dir(executable), "tmux-ghostty-broker")
	if _, err := os.Stat(sibling); err == nil {
		return sibling, nil, nil
	}
	return executable, []string{"serve-broker"}, nil
}

func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func cleanupStaleRuntime(paths Paths) error {
	if _, err := os.Stat(paths.SocketPath); err == nil {
		client := rpc.NewClient(paths.SocketPath)
		if err := client.Call(context.Background(), "broker.status", nil, &struct{}{}); err != nil {
			_ = os.Remove(paths.SocketPath)
		}
	}
	if pid, err := ReadPID(paths.PIDPath); err == nil && !ProcessAlive(pid) {
		_ = os.Remove(paths.PIDPath)
		_ = os.Remove(paths.SocketPath)
	}
	return nil
}

func startBroker(paths Paths) error {
	command, args, err := BrokerCommand(paths)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(paths.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := exec.Command(command, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "TMUX_GHOSTTY_BROKER_SOCKET="+paths.SocketPath)
	return cmd.Start()
}
