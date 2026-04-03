package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFindRequiredAssets(t *testing.T) {
	release := Release{
		TagName: "v1.2.3",
		Assets: []Asset{
			{Name: "tmux-ghostty_v1.2.3_darwin_universal.pkg"},
			{Name: "checksums.txt"},
		},
	}

	packageAsset, checksumsAsset, err := FindRequiredAssets(release)
	if err != nil {
		t.Fatalf("FindRequiredAssets() error = %v", err)
	}
	if packageAsset.Name != "tmux-ghostty_v1.2.3_darwin_universal.pkg" {
		t.Fatalf("unexpected package asset: %#v", packageAsset)
	}
	if checksumsAsset.Name != "checksums.txt" {
		t.Fatalf("unexpected checksums asset: %#v", checksumsAsset)
	}
}

func TestParseChecksumsAndVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.pkg")
	payload := []byte("payload")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	sum := sha256.Sum256(payload)
	checksums := ParseChecksums([]byte(hex.EncodeToString(sum[:]) + "  sample.pkg\n"))
	if got := checksums["sample.pkg"]; got == "" {
		t.Fatalf("expected checksum entry, got %#v", checksums)
	}
	if err := VerifyChecksum(path, checksums["sample.pkg"]); err != nil {
		t.Fatalf("VerifyChecksum() error = %v", err)
	}
}

func TestLatestReleaseAndDownloadFile(t *testing.T) {
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/repo/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":"v9.9.9","assets":[{"name":"tmux-ghostty_v9.9.9_darwin_universal.pkg","browser_download_url":"` + serverURL + `/assets/pkg"},{"name":"checksums.txt","browser_download_url":"` + serverURL + `/assets/checksums"}]}`))
		case "/assets/pkg":
			_, _ = w.Write([]byte("pkg-bytes"))
		case "/assets/checksums":
			_, _ = w.Write([]byte("ignored"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	client := NewGitHubClient("example/repo")
	client.APIBaseURL = server.URL

	release, err := client.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease() error = %v", err)
	}
	if release.TagName != "v9.9.9" {
		t.Fatalf("LatestRelease() tag = %q", release.TagName)
	}

	path := filepath.Join(t.TempDir(), "pkg")
	if err := client.DownloadFile(context.Background(), server.URL+"/assets/pkg", path); err != nil {
		t.Fatalf("DownloadFile() error = %v", err)
	}
	if data, err := os.ReadFile(path); err != nil {
		t.Fatalf("read download: %v", err)
	} else if string(data) != "pkg-bytes" {
		t.Fatalf("downloaded bytes = %q", string(data))
	}
}
