package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/guyuanshun/tmux-ghostty/internal/buildinfo"
	"github.com/guyuanshun/tmux-ghostty/internal/install"
)

const defaultAPIBaseURL = "https://api.github.com"

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type GitHubClient struct {
	HTTPClient *http.Client
	APIBaseURL string
	Repo       string
	Token      string
}

func NewGitHubClient(repo string) *GitHubClient {
	return &GitHubClient{
		HTTPClient: http.DefaultClient,
		APIBaseURL: defaultAPIBaseURL,
		Repo:       repo,
		Token:      githubToken(),
	}
}

func (c *GitHubClient) LatestRelease(ctx context.Context) (Release, error) {
	return c.fetchRelease(ctx, "/repos/"+c.Repo+"/releases/latest")
}

func (c *GitHubClient) ReleaseByTag(ctx context.Context, tag string) (Release, error) {
	return c.fetchRelease(ctx, "/repos/"+c.Repo+"/releases/tags/"+tag)
}

func (c *GitHubClient) DownloadFile(ctx context.Context, downloadURL string, dst string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	c.setHeaders(request)

	response, err := c.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("download %s failed: %s: %s", downloadURL, response.Status, strings.TrimSpace(string(body)))
	}

	file, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := io.Copy(file, response.Body); err != nil {
		return err
	}
	return nil
}

func FindRequiredAssets(release Release) (Asset, Asset, error) {
	packageAsset, ok := FindAsset(release, install.PackageAssetName(release.TagName))
	if !ok {
		return Asset{}, Asset{}, fmt.Errorf("release %s does not include %s", release.TagName, install.PackageAssetName(release.TagName))
	}
	checksumsAsset, ok := FindAsset(release, install.ChecksumsAssetName)
	if !ok {
		return Asset{}, Asset{}, fmt.Errorf("release %s does not include %s", release.TagName, install.ChecksumsAssetName)
	}
	return packageAsset, checksumsAsset, nil
}

func FindAsset(release Release, name string) (Asset, bool) {
	for _, asset := range release.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return Asset{}, false
}

func ParseChecksums(data []byte) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		result[filepath.Base(name)] = strings.ToLower(fields[0])
	}
	return result
}

func VerifyChecksum(path string, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch for %s: got %s want %s", path, actual, expected)
	}
	return nil
}

func githubToken() string {
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(os.Getenv("GH_TOKEN"))
}

func (c *GitHubClient) fetchRelease(ctx context.Context, path string) (Release, error) {
	if strings.TrimSpace(c.Repo) == "" {
		return Release{}, fmt.Errorf("github release repo is not configured")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.apiBaseURL(), "/")+path, nil)
	if err != nil {
		return Release{}, err
	}
	c.setHeaders(request)

	response, err := c.httpClient().Do(request)
	if err != nil {
		return Release{}, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return Release{}, fmt.Errorf("github release lookup failed: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var release Release
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return Release{}, err
	}
	if strings.TrimSpace(release.TagName) == "" {
		return Release{}, fmt.Errorf("github release response did not include tag_name")
	}
	return release, nil
}

func (c *GitHubClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *GitHubClient) apiBaseURL() string {
	if strings.TrimSpace(c.APIBaseURL) != "" {
		return c.APIBaseURL
	}
	return defaultAPIBaseURL
}

func (c *GitHubClient) setHeaders(request *http.Request) {
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "tmux-ghostty/"+buildinfo.Version)
	if token := strings.TrimSpace(c.Token); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
}
