package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guyuanshun/tmux-ghostty/internal/buildinfo"
)

const (
	BinaryName         = "tmux-ghostty"
	BrokerBinaryName   = "tmux-ghostty-broker"
	DefaultInstallDir  = "/usr/local/bin"
	ChecksumsAssetName = "checksums.txt"
)

func InstallDir() string {
	if dir := strings.TrimSpace(os.Getenv("TMUX_GHOSTTY_INSTALL_DIR")); dir != "" {
		return dir
	}
	return DefaultInstallDir
}

func MainBinaryPath() string {
	return filepath.Join(InstallDir(), BinaryName)
}

func BrokerBinaryPath() string {
	return filepath.Join(InstallDir(), BrokerBinaryName)
}

func ReleaseRepo() string {
	if repo := strings.TrimSpace(os.Getenv("TMUX_GHOSTTY_RELEASE_REPO")); repo != "" {
		return repo
	}
	return buildinfo.ReleaseRepo
}

func PackageID() string {
	if packageID := strings.TrimSpace(os.Getenv("TMUX_GHOSTTY_PACKAGE_ID")); packageID != "" {
		return packageID
	}
	return buildinfo.PackageID
}

func PackageAssetName(version string) string {
	return fmt.Sprintf("tmux-ghostty_%s_darwin_universal.pkg", version)
}

func ArchiveAssetName(version string) string {
	return fmt.Sprintf("tmux-ghostty_%s_darwin_universal.tar.gz", version)
}
