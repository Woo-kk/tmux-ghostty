package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	DefaultIdleTimeout = 300 * time.Second
)

type Paths struct {
	BaseDir      string
	SocketPath   string
	PIDPath      string
	StatePath    string
	ActionsPath  string
	LogPath      string
	BrokerBinary string
}

func DefaultPaths() (Paths, error) {
	baseDir := os.Getenv("TMUX_GHOSTTY_HOME")
	if baseDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve user home: %w", err)
		}
		baseDir = filepath.Join(homeDir, "Library", "Application Support", "tmux-ghostty")
	}
	return Paths{
		BaseDir:      baseDir,
		SocketPath:   filepath.Join(baseDir, "broker.sock"),
		PIDPath:      filepath.Join(baseDir, "broker.pid"),
		StatePath:    filepath.Join(baseDir, "state.json"),
		ActionsPath:  filepath.Join(baseDir, "actions.json"),
		LogPath:      filepath.Join(baseDir, "broker.log"),
		BrokerBinary: os.Getenv("TMUX_GHOSTTY_BROKER_BIN"),
	}, nil
}

func (p Paths) EnsureBaseDir() error {
	return os.MkdirAll(p.BaseDir, 0o755)
}

func IdleTimeout() time.Duration {
	value := os.Getenv("TMUX_GHOSTTY_IDLE_TIMEOUT")
	if value == "" {
		return DefaultIdleTimeout
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return DefaultIdleTimeout
	}
	return time.Duration(seconds) * time.Second
}

func ReadPID(path string) (int, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(string(bytesTrimSpace(buf)))
	if err != nil {
		return 0, err
	}
	return pid, nil
}

func bytesTrimSpace(buf []byte) string {
	start := 0
	for start < len(buf) && (buf[start] == ' ' || buf[start] == '\n' || buf[start] == '\t' || buf[start] == '\r') {
		start++
	}
	end := len(buf)
	for end > start && (buf[end-1] == ' ' || buf[end-1] == '\n' || buf[end-1] == '\t' || buf[end-1] == '\r') {
		end--
	}
	return string(buf[start:end])
}
