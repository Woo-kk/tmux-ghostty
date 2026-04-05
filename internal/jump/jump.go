package jump

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/execx"
	"github.com/Woo-kk/tmux-ghostty/internal/model"
)

const (
	defaultRemoteSession = "tmux-ghostty"

	ResolvedViaDirectQuery    = "direct_query"
	ResolvedViaHostListSearch = "host_list_search"

	AttachReasonPasswordPrompt      = "password_prompt"
	AttachReasonQueryNoResult       = "query_no_result"
	AttachReasonAccountAmbiguous    = "account_ambiguous"
	AttachReasonRemoteShellNotReady = "remote_shell_not_ready"
	AttachReasonStageTimeout        = "stage_timeout"
	AttachReasonUnknownStage        = "unknown_stage"
)

var (
	ansiRE         = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	controlRE      = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f]`)
	remotePromptRE = regexp.MustCompile(`(?m)^(?:\([^)]+\)\s*)?(?:\[[^\]]+\][#$%]|[^\s@]+@[^\s:]+[: ][^\n]*[#$]|[^ \t]+[#$%])\s*$`)
	assetPromptRE  = regexp.MustCompile(`资产\[(.+?)\(([^)]+)\)\]`)
	accountRowRE   = regexp.MustCompile(`(?m)^\s*(\d+)\s+\|\s+([^\|]+?)\s+\|\s+([^\|]+?)\s*$`)
)

type tmuxController interface {
	SendKeys(target string, text string) error
	SendCtrlC(target string) error
	CapturePane(target string, lines int) (string, error)
}

type HostMatch struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Address     string `json:"address"`
}

type ResolvedHost struct {
	Query         string            `json:"query"`
	Name          string            `json:"name"`
	Address       string            `json:"address"`
	AccountID     string            `json:"account_id"`
	AccountLabel  string            `json:"account_label"`
	RemoteSession string            `json:"remote_session"`
	ResolvedVia   string            `json:"resolved_via"`
	StageTrace    []model.PaneStage `json:"stage_trace"`
}

type AttachError struct {
	Reason     string            `json:"reason"`
	Stage      model.PaneStage   `json:"stage,omitempty"`
	StageTrace []model.PaneStage `json:"stage_trace,omitempty"`
	Candidates []string          `json:"candidates,omitempty"`
	Detail     string            `json:"detail"`
}

func (e *AttachError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return e.Detail
	}
	return e.Reason
}

func (e *AttachError) RPCData() any {
	if e == nil {
		return nil
	}
	return map[string]any{
		"reason":      e.Reason,
		"stage":       e.Stage,
		"stage_trace": e.StageTrace,
		"candidates":  e.Candidates,
		"detail":      e.Detail,
	}
}

type accountCandidate struct {
	ID      string
	Label   string
	Details string
}

type Client struct {
	tmux          tmuxController
	profilePath   string
	runnerScript  string
	remoteSession string
}

func New(client tmuxController) *Client {
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

func (c *Client) AttachHost(localTarget string, hostQuery string) (ResolvedHost, error) {
	if err := c.validate(); err != nil {
		return ResolvedHost{}, err
	}

	query := strings.TrimSpace(hostQuery)
	if query == "" {
		return ResolvedHost{}, fmt.Errorf("empty host query")
	}

	if err := c.tmux.SendCtrlC(localTarget); err != nil {
		// Ignore pre-attach interrupt failures on a fresh shell.
	}

	command := execx.ShellQuote(c.runnerScript) + " " + execx.ShellQuote(c.profilePath) + " 1"
	if err := c.tmux.SendKeys(localTarget, command); err != nil {
		return ResolvedHost{}, fmt.Errorf("start jump profile: %w", err)
	}

	trace := []model.PaneStage{}
	snapshot, stage, err := c.waitForStage(localTarget, 45*time.Second, model.StageJumpMenu, model.StageHostSearch, model.StageAccountSelect, model.StageRemoteShell, model.StagePasswordPrompt)
	if err != nil {
		return ResolvedHost{}, newAttachError(AttachReasonStageTimeout, model.StageUnknown, trace, nil, err.Error())
	}
	trace = appendTrace(trace, stage)
	if stage == model.StagePasswordPrompt {
		return ResolvedHost{}, newAttachError(AttachReasonPasswordPrompt, stage, trace, nil, "jumpserver requires manual password entry")
	}

	resolvedVia := ResolvedViaDirectQuery
	switch stage {
	case model.StageJumpMenu, model.StageHostSearch:
		if err := c.tmux.SendKeys(localTarget, query); err != nil {
			return ResolvedHost{}, fmt.Errorf("search host: %w", err)
		}
		snapshot, stage, err = c.waitForStage(localTarget, 25*time.Second, model.StageJumpMenu, model.StageHostSearch, model.StageAccountSelect, model.StageRemoteShell, model.StagePasswordPrompt)
		if err != nil {
			return ResolvedHost{}, newAttachError(AttachReasonStageTimeout, model.StageUnknown, trace, nil, err.Error())
		}
		trace = appendTrace(trace, stage)
	}

	if stage == model.StagePasswordPrompt {
		return ResolvedHost{}, newAttachError(AttachReasonPasswordPrompt, stage, trace, nil, "jumpserver requires manual password entry")
	}

	if stage == model.StageJumpMenu || (stage == model.StageHostSearch && containsNoAssets(snapshot)) {
		resolvedVia = ResolvedViaHostListSearch
		snapshot, stage, err = c.enterHostList(localTarget, stage, trace)
		if err != nil {
			return ResolvedHost{}, err
		}
		trace = appendTrace(trace, stage)
		if err := c.tmux.SendKeys(localTarget, query); err != nil {
			return ResolvedHost{}, fmt.Errorf("search host in host list: %w", err)
		}
		snapshot, stage, err = c.waitForStage(localTarget, 25*time.Second, model.StageHostSearch, model.StageAccountSelect, model.StageRemoteShell, model.StagePasswordPrompt)
		if err != nil {
			return ResolvedHost{}, newAttachError(AttachReasonStageTimeout, model.StageUnknown, trace, nil, err.Error())
		}
		trace = appendTrace(trace, stage)
	}

	if stage == model.StagePasswordPrompt {
		return ResolvedHost{}, newAttachError(AttachReasonPasswordPrompt, stage, trace, nil, "jumpserver requires manual password entry")
	}
	if stage == model.StageHostSearch {
		return ResolvedHost{}, newAttachError(AttachReasonQueryNoResult, stage, trace, nil, "host query returned no attachable result")
	}
	if stage == model.StageJumpMenu {
		return ResolvedHost{}, newAttachError(AttachReasonUnknownStage, stage, trace, nil, "jumpserver returned to menu without resolving a host")
	}

	accountID := ""
	accountLabel := ""
	if stage == model.StageAccountSelect {
		candidates := parseAccountCandidates(snapshot)
		if len(candidates) == 0 {
			return ResolvedHost{}, newAttachError(AttachReasonAccountAmbiguous, stage, trace, nil, "multiple accounts present but selectable rows could not be parsed")
		}
		selected, err := chooseAccount(candidates)
		if err != nil {
			return ResolvedHost{}, newAttachError(AttachReasonAccountAmbiguous, stage, trace, accountLabels(candidates), err.Error())
		}
		accountID = selected.ID
		accountLabel = selected.Label
		if err := c.tmux.SendKeys(localTarget, accountID); err != nil {
			return ResolvedHost{}, fmt.Errorf("select account: %w", err)
		}
		snapshot, stage, err = c.waitForStage(localTarget, 30*time.Second, model.StageRemoteShell, model.StagePasswordPrompt)
		if err != nil {
			return ResolvedHost{}, newAttachError(AttachReasonStageTimeout, model.StageUnknown, trace, nil, err.Error())
		}
		trace = appendTrace(trace, stage)
	}

	if stage == model.StagePasswordPrompt {
		return ResolvedHost{}, newAttachError(AttachReasonPasswordPrompt, stage, trace, nil, "jumpserver requires manual password entry")
	}
	if stage != model.StageRemoteShell {
		return ResolvedHost{}, newAttachError(AttachReasonRemoteShellNotReady, stage, trace, nil, "jumpserver did not reach a remote shell")
	}

	name, address := parseAsset(snapshot)
	if err := c.EnsureRemoteTmux(localTarget, c.remoteSession); err != nil {
		return ResolvedHost{}, err
	}

	return ResolvedHost{
		Query:         query,
		Name:          coalesce(name, query),
		Address:       address,
		AccountID:     accountID,
		AccountLabel:  accountLabel,
		RemoteSession: c.remoteSession,
		ResolvedVia:   resolvedVia,
		StageTrace:    trace,
	}, nil
}

func (c *Client) EnsureRemoteTmux(localTarget string, remoteSession string) error {
	if remoteSession == "" {
		remoteSession = c.remoteSession
	}
	command := "tmux has-session -t " + execx.ShellQuote(remoteSession) +
		" 2>/dev/null || tmux new-session -d -s " + execx.ShellQuote(remoteSession) +
		"; exec tmux attach-session -t " + execx.ShellQuote(remoteSession)
	if err := c.tmux.SendKeys(localTarget, command); err != nil {
		return fmt.Errorf("attach remote tmux: %w", err)
	}
	_, stage, err := c.waitForStage(localTarget, 15*time.Second, model.StageRemoteShell)
	if err != nil {
		return newAttachError(AttachReasonStageTimeout, model.StageUnknown, []model.PaneStage{model.StageRemoteShell}, nil, err.Error())
	}
	if stage != model.StageRemoteShell {
		return newAttachError(AttachReasonRemoteShellNotReady, stage, []model.PaneStage{stage}, nil, "remote tmux did not become ready")
	}
	return nil
}

func (c *Client) Reconnect(localTarget string) error {
	return c.tmux.SendKeys(localTarget, "")
}

func DetectStage(text string) model.PaneStage {
	cleaned := sanitizeSnapshot(text)
	tail := tailLines(cleaned, 80)
	last := lastNonEmptyLine(tail)
	if remotePromptRE.MatchString(last) {
		return model.StageRemoteShell
	}

	lowerTail := strings.ToLower(tail)
	switch {
	case strings.Contains(tail, "[Host]>") || strings.Contains(lowerTail, "search:"):
		return model.StageHostSearch
	case accountRowRE.MatchString(tail) || strings.Contains(tail, "ID>"):
		return model.StageAccountSelect
	case strings.Contains(tail, "Opt>"):
		return model.StageJumpMenu
	case strings.Contains(tail, "开始连接到") || strings.Contains(tail, "Connecting to"):
		return model.StageConnecting
	case strings.Contains(lowerTail, "password") || strings.Contains(lowerTail, "verification code") || strings.Contains(lowerTail, "passcode"):
		return model.StagePasswordPrompt
	default:
		return model.StageUnknown
	}
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

func (c *Client) waitForStage(localTarget string, timeout time.Duration, expected ...model.PaneStage) (string, model.PaneStage, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		text, err := c.tmux.CapturePane(localTarget, 220)
		if err != nil {
			return "", model.StageUnknown, err
		}
		stage := DetectStage(text)
		for _, candidate := range expected {
			if stage == candidate {
				return text, stage, nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	return "", model.StageUnknown, fmt.Errorf("timed out waiting for jumpserver stage %v", expected)
}

func (c *Client) enterHostList(localTarget string, currentStage model.PaneStage, trace []model.PaneStage) (string, model.PaneStage, error) {
	if currentStage == model.StageHostSearch {
		return "", model.StageHostSearch, nil
	}
	if err := c.tmux.SendKeys(localTarget, "h"); err != nil {
		return "", model.StageUnknown, fmt.Errorf("enter host list: %w", err)
	}
	snapshot, stage, err := c.waitForStage(localTarget, 15*time.Second, model.StageHostSearch, model.StagePasswordPrompt)
	if err != nil {
		return "", model.StageUnknown, newAttachError(AttachReasonStageTimeout, model.StageUnknown, trace, nil, err.Error())
	}
	if stage == model.StagePasswordPrompt {
		return "", stage, newAttachError(AttachReasonPasswordPrompt, stage, appendTrace(trace, stage), nil, "jumpserver requires manual password entry")
	}
	return snapshot, stage, nil
}

func sanitizeSnapshot(text string) string {
	cleaned := ansiRE.ReplaceAllString(text, "")
	cleaned = strings.ReplaceAll(cleaned, "\r", "")
	return controlRE.ReplaceAllString(cleaned, "")
}

func tailLines(text string, count int) string {
	lines := strings.Split(text, "\n")
	if count <= 0 || len(lines) <= count {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-count:], "\n")
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line != "" {
			return line
		}
	}
	return ""
}

func appendTrace(trace []model.PaneStage, stage model.PaneStage) []model.PaneStage {
	if len(trace) == 0 || trace[len(trace)-1] != stage {
		return append(trace, stage)
	}
	return trace
}

func parseAsset(text string) (string, string) {
	match := assetPromptRE.FindStringSubmatch(sanitizeSnapshot(text))
	if len(match) != 3 {
		return "", ""
	}
	return strings.TrimSpace(match[1]), strings.TrimSpace(match[2])
}

func parseAccountCandidates(text string) []accountCandidate {
	matches := accountRowRE.FindAllStringSubmatch(sanitizeSnapshot(text), -1)
	out := make([]accountCandidate, 0, len(matches))
	for _, match := range matches {
		if len(match) != 4 {
			continue
		}
		out = append(out, accountCandidate{
			ID:      strings.TrimSpace(match[1]),
			Label:   strings.TrimSpace(match[2]),
			Details: strings.TrimSpace(match[3]),
		})
	}
	return out
}

func chooseAccount(candidates []accountCandidate) (accountCandidate, error) {
	if len(candidates) == 0 {
		return accountCandidate{}, fmt.Errorf("account selection prompt not understood")
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	for _, candidate := range candidates {
		value := strings.ToLower(candidate.Label + " " + candidate.Details)
		if strings.Contains(value, "root") {
			return candidate, nil
		}
	}
	return accountCandidate{}, fmt.Errorf("multiple accounts found and none looked like root")
}

func accountLabels(candidates []accountCandidate) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		label := candidate.Label
		if candidate.Details != "" {
			label = label + " | " + candidate.Details
		}
		out = append(out, strings.TrimSpace(label))
	}
	return out
}

func containsNoAssets(text string) bool {
	lower := strings.ToLower(sanitizeSnapshot(text))
	return strings.Contains(lower, "no assets") || strings.Contains(lower, "没有资产") || strings.Contains(lower, "无资产")
}

func newAttachError(reason string, stage model.PaneStage, trace []model.PaneStage, candidates []string, detail string) error {
	return &AttachError{
		Reason:     reason,
		Stage:      stage,
		StageTrace: append([]model.PaneStage(nil), trace...),
		Candidates: append([]string(nil), candidates...),
		Detail:     detail,
	}
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
