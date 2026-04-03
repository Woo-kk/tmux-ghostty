package install

import "testing"

func TestAssetNames(t *testing.T) {
	version := "v1.2.3"
	if got := PackageAssetName(version); got != "tmux-ghostty_v1.2.3_darwin_universal.pkg" {
		t.Fatalf("PackageAssetName() = %q", got)
	}
	if got := ArchiveAssetName(version); got != "tmux-ghostty_v1.2.3_darwin_universal.tar.gz" {
		t.Fatalf("ArchiveAssetName() = %q", got)
	}
}
