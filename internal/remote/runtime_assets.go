package remote

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

const bundledRunnerDir = "runtime/jumpserver"

//go:embed assets/run_jump_profile.sh assets/jump_connect.exp
var bundledRunnerAssets embed.FS

func ensureBundledRunner() (string, error) {
	baseDir, err := resolveRuntimeBaseDir()
	if err != nil {
		return "", err
	}
	assetDir := filepath.Join(baseDir, bundledRunnerDir)
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return "", fmt.Errorf("create jump runner asset directory: %w", err)
	}

	runnerPath := filepath.Join(assetDir, "run_jump_profile.sh")
	if err := materializeBundledRunnerAsset("assets/run_jump_profile.sh", runnerPath, 0o755); err != nil {
		return "", err
	}
	expectPath := filepath.Join(assetDir, "jump_connect.exp")
	if err := materializeBundledRunnerAsset("assets/jump_connect.exp", expectPath, 0o755); err != nil {
		return "", err
	}
	return runnerPath, nil
}

func materializeBundledRunnerAsset(name string, path string, mode os.FileMode) error {
	content, err := bundledRunnerAssets.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read bundled jump asset %s: %w", name, err)
	}

	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, content) {
			if err := os.Chmod(path, mode); err != nil {
				return fmt.Errorf("chmod bundled jump asset %s: %w", path, err)
			}
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read bundled jump asset %s: %w", path, err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, content, mode); err != nil {
		return fmt.Errorf("write bundled jump asset %s: %w", path, err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod bundled jump asset %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("install bundled jump asset %s: %w", path, err)
	}
	return nil
}

func resolveRuntimeBaseDir() (string, error) {
	baseDir := os.Getenv("TMUX_GHOSTTY_HOME")
	if baseDir != "" {
		return baseDir, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(homeDir, "Library", "Application Support", "tmux-ghostty"), nil
}
