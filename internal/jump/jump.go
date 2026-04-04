package jump

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/execx"
	"github.com/Woo-kk/tmux-ghostty/internal/tmux"
)

const (
	defaultRemoteSession = "tmux-ghostty"
	stageJump            = "jump_menu"
	stageHost            = "host_select"
	stageAccount         = "account_select"
	stageRemote          = "remote_shell"
	stageConnecting      = "connecting"
	stagePassword        = "password_prompt"
	stageUnknown         = "unknown"
)

var (
	remotePromptRE = regexp.MustCompile(`(?m)^(?:\([^)]+\)\s*)?(?:\[[^\]]+\][#$%]|[^\s@]+@[^\s:]+[: ][^\n]*[#$]|[^ \t]+[#$%])\s*$`)
	assetPromptRE  = regexp.MustCompile(`资产\[(.+?)\(([^)]+)\)\]`)
	accountRowRE   = regexp.MustCompile(`(?m)^\s*(\d+)\s+\|\s+([^\|]+?)\s+\|\s+([^\|]+?)\s*$`)
)

type HostMatch struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Address     string `json:"address"`
}

type ResolvedHost struct {
	Query         string `json:"query"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	AccountID     string `json:"account_id"`
	AccountLabel  string `json:"account_label"`
	RemoteSession string `json:"remote_session"`
}

type Client struct {
	tmux          *tmux.Client
	profilePath   string
	runnerScript  string
	remoteSession string
}

func New(client *tmux.Client) *Client {
	return &Client{
		tmux:          client,
		profilePath:   resolveProfilePath(),
		runnerScript:  resolveRunnerPath(),
		remoteSession: resolveRemoteSession(),
	}
}

func (c *Client) SearchHost(query string) ([]HostMatch, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("empty host query")
	}
	if c.profilePath == "" || c.runnerScript == "" {
		return []HostMatch{{DisplayName: query}}, nil
	}
	return []HostMatch{{DisplayName: query}}, nil
}

func (c *Client) AttachHost(localSession string, hostQuery string) (ResolvedHost, error) {
	if err := c.validate(); err != nil {
		return ResolvedHost{}, err
	}

	if err := c.tmux.SendCtrlC(localSession); err != nil {
		// Ignore pre-attach interrupt failures on a fresh shell.
	}

	command := execx.ShellQuote(c.runnerScript) + " " + execx.ShellQuote(c.profilePath) + " 1"
	if err := c.tmux.SendKeys(localSession, command); err != nil {
		return ResolvedHost{}, fmt.Errorf("start jump profile: %w", err)
	}

	snapshot, stage, err := c.waitForStage(localSession, 45*time.Second, stageJump, stageHost, stageAccount, stageRemote, stagePassword)
	if err != nil {
		return ResolvedHost{}, err
	}
	if stage == stagePassword {
		return ResolvedHost{}, fmt.Errorf("jumpserver requires manual password entry")
	}
	if stage == stageJump || stage == stageHost {
		if err := c.tmux.SendKeys(localSession, hostQuery); err != nil {
			return ResolvedHost{}, fmt.Errorf("search host: %w", err)
		}
		snapshot, stage, err = c.waitForStage(localSession, 25*time.Second, stageHost, stageAccount, stageRemote, stagePassword)
		if err != nil {
			return ResolvedHost{}, err
		}
	}
	if stage == stagePassword {
		return ResolvedHost{}, fmt.Errorf("jumpserver requires manual password entry")
	}
	if stage == stageHost {
		return ResolvedHost{}, fmt.Errorf("host query is ambiguous, refine the query")
	}

	accountID := ""
	accountLabel := ""
	if stage == stageAccount {
		accountID, accountLabel, err = selectPreferredAccount(snapshot)
		if err != nil {
			return ResolvedHost{}, err
		}
		if err := c.tmux.SendKeys(localSession, accountID); err != nil {
			return ResolvedHost{}, fmt.Errorf("select account: %w", err)
		}
		snapshot, stage, err = c.waitForStage(localSession, 30*time.Second, stageRemote, stagePassword)
		if err != nil {
			return ResolvedHost{}, err
		}
	}
	if stage == stagePassword {
		return ResolvedHost{}, fmt.Errorf("jumpserver requires manual password entry")
	}
	if stage != stageRemote {
		return ResolvedHost{}, fmt.Errorf("jumpserver did not reach remote shell")
	}

	name, address := parseAsset(snapshot)
	if err := c.EnsureRemoteTmux(localSession, c.remoteSession); err != nil {
		return ResolvedHost{}, err
	}

	return ResolvedHost{
		Query:         hostQuery,
		Name:          coalesce(name, hostQuery),
		Address:       address,
		AccountID:     accountID,
		AccountLabel:  accountLabel,
		RemoteSession: c.remoteSession,
	}, nil
}

func (c *Client) EnsureRemoteTmux(localSession string, remoteSession string) error {
	if remoteSession == "" {
		remoteSession = c.remoteSession
	}
	command := "tmux has-session -t " + execx.ShellQuote(remoteSession) +
		" 2>/dev/null || tmux new-session -d -s " + execx.ShellQuote(remoteSession) +
		"; exec tmux attach-session -t " + execx.ShellQuote(remoteSession)
	if err := c.tmux.SendKeys(localSession, command); err != nil {
		return fmt.Errorf("attach remote tmux: %w", err)
	}
	_, stage, err := c.waitForStage(localSession, 15*time.Second, stageRemote)
	if err != nil {
		return err
	}
	if stage != stageRemote {
		return fmt.Errorf("remote tmux did not become ready")
	}
	return nil
}

func (c *Client) Reconnect(localSession string) error {
	return c.tmux.SendKeys(localSession, "")
}

func (c *Client) validate() error {
	if c.profilePath == "" {
		return fmt.Errorf("jump profile not configured; set TMUX_GHOSTTY_JUMP_PROFILE")
	}
	if c.runnerScript == "" {
		return fmt.Errorf("jump runner not configured")
	}
	if _, err := os.Stat(c.profilePath); err != nil {
		return fmt.Errorf("jump profile unavailable: %w", err)
	}
	if _, err := os.Stat(c.runnerScript); err != nil {
		return fmt.Errorf("jump runner unavailable: %w", err)
	}
	return nil
}

func (c *Client) waitForStage(localSession string, timeout time.Duration, expected ...string) (string, string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		text, err := c.tmux.CapturePane(localSession, 220)
		if err != nil {
			return "", "", err
		}
		stage := detectStage(text)
		for _, candidate := range expected {
			if stage == candidate {
				return text, stage, nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	return "", "", fmt.Errorf("timed out waiting for jumpserver stage %v", expected)
}

func detectStage(text string) string {
	lines := strings.Split(text, "\n")
	last := ""
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		last = line
		break
	}
	if remotePromptRE.MatchString(last) {
		return stageRemote
	}
	switch {
	case strings.Contains(text, "Opt>"):
		return stageJump
	case strings.Contains(text, "[Host]>"):
		return stageHost
	case accountRowRE.MatchString(text) || strings.Contains(text, "ID>"):
		return stageAccount
	case strings.Contains(text, "开始连接到"), strings.Contains(text, "Connecting to"):
		return stageConnecting
	case strings.Contains(strings.ToLower(text), "password"), strings.Contains(strings.ToLower(text), "verification code"), strings.Contains(strings.ToLower(text), "passcode"):
		return stagePassword
	default:
		return stageUnknown
	}
}

func parseAsset(text string) (string, string) {
	match := assetPromptRE.FindStringSubmatch(text)
	if len(match) != 3 {
		return "", ""
	}
	return strings.TrimSpace(match[1]), strings.TrimSpace(match[2])
}

func selectPreferredAccount(text string) (string, string, error) {
	matches := accountRowRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		if strings.Contains(text, "ID>") {
			return "", "", fmt.Errorf("multiple accounts present but could not parse selectable rows")
		}
		return "", "", fmt.Errorf("account selection prompt not understood")
	}
	if len(matches) == 1 {
		return strings.TrimSpace(matches[0][1]), strings.TrimSpace(matches[0][2]), nil
	}
	for _, match := range matches {
		label := strings.ToLower(strings.TrimSpace(match[2]))
		if strings.Contains(label, "root") {
			return strings.TrimSpace(match[1]), strings.TrimSpace(match[2]), nil
		}
	}
	return "", "", fmt.Errorf("multiple accounts found and none looked like root")
}

func resolveProfilePath() string {
	if value := os.Getenv("TMUX_GHOSTTY_JUMP_PROFILE"); value != "" {
		return value
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	defaultPath := filepath.Join(homeDir, ".config", "codex-jumpserver", "profiles", "default.env")
	if _, err := os.Stat(defaultPath); err == nil {
		return defaultPath
	}
	return ""
}

func resolveRunnerPath() string {
	if value := os.Getenv("TMUX_GHOSTTY_JUMP_RUNNER"); value != "" {
		return value
	}
	defaultPath := "/Users/guyuanshun/.codex/skills/tmux-jumpserver/scripts/run_jump_profile.sh"
	if _, err := os.Stat(defaultPath); err == nil {
		return defaultPath
	}
	return ""
}

func resolveRemoteSession() string {
	if value := os.Getenv("TMUX_GHOSTTY_REMOTE_TMUX_SESSION"); value != "" {
		return value
	}
	return defaultRemoteSession
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
