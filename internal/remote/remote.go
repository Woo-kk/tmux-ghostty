package remote

import (
	"errors"
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
	ProviderJumpServer   = "jumpserver"

	ResolvedViaDirectQuery      = "direct_query"
	ResolvedViaTargetListSearch = "target_list_search"

	AttachReasonAuthPrompt          = "auth_prompt"
	AttachReasonQueryNoResult       = "query_no_result"
	AttachReasonSelectionAmbiguous  = "selection_ambiguous"
	AttachReasonRemoteShellNotReady = "remote_shell_not_ready"
	AttachReasonStageTimeout        = "stage_timeout"
	AttachReasonUnknownStage        = "unknown_stage"

	remoteTmuxMarkerPrefix     = "__TMUX_GHOSTTY_REMOTE_TMUX__"
	remoteTmuxProbeDelay       = time.Second
	remoteTmuxOutcomeSettleFor = 1500 * time.Millisecond
)

var (
	ansiRE             = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	controlRE          = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f]`)
	remotePromptRE     = regexp.MustCompile(`(?m)^(?:\([^)]+\)\s*)?(?:\[[^\]]+\][#$%]|[^\s@]+@[^\s:]+[: ][^\n]*[#$]|[^ \t]+[#$%])\s*$`)
	assetPromptRE      = regexp.MustCompile(`资产\[(.+?)\(([^)]+)\)\]`)
	accountRowRE       = regexp.MustCompile(`(?m)^\s*(\d+)\s+\|\s+([^\|]+?)\s+\|\s+([^\|]+?)\s*$`)
	menuPromptTypedHRE = regexp.MustCompile(`(?m)^Opt>\s*h$`)
)

type tmuxController interface {
	SendKeys(target string, text string) error
	SendText(target string, text string) error
	SendCtrlC(target string) error
	CapturePane(target string, lines int) (string, error)
}

type TargetMatch struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Address     string `json:"address"`
}

type ResolvedTarget struct {
	Query            string                 `json:"query"`
	Name             string                 `json:"name"`
	Address          string                 `json:"address"`
	SelectionID      string                 `json:"selection_id"`
	SelectionLabel   string                 `json:"selection_label"`
	RemoteSession    string                 `json:"remote_session"`
	RemoteTmuxStatus model.RemoteTmuxStatus `json:"remote_tmux_status"`
	RemoteTmuxDetail string                 `json:"remote_tmux_detail,omitempty"`
	Provider         string                 `json:"provider"`
	ResolvedVia      string                 `json:"resolved_via"`
	StageTrace       []model.PaneStage      `json:"stage_trace"`
}

type remoteTmuxOutcome struct {
	Session string
	Status  model.RemoteTmuxStatus
	Detail  string
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
	provider provider
}

type provider interface {
	searchTarget(query string) ([]TargetMatch, error)
	attachTarget(localTarget string, query string) (ResolvedTarget, error)
	ensureRemoteSession(localTarget string, remoteSession string) error
	reconnect(localTarget string) error
	detectStage(text string) model.PaneStage
}

type jumpServerProvider struct {
	tmux          tmuxController
	profilePath   string
	runnerScript  string
	runnerErr     error
	remoteSession string
}

func New(client tmuxController) *Client {
	return &Client{
		provider: newProvider(client),
	}
}

func newProvider(client tmuxController) provider {
	providerName := strings.ToLower(strings.TrimSpace(os.Getenv("TMUX_GHOSTTY_REMOTE_PROVIDER")))
	switch providerName {
	case "", ProviderJumpServer:
		runnerScript, runnerErr := resolveRunnerPath()
		return &jumpServerProvider{
			tmux:          client,
			profilePath:   resolveProfilePath(),
			runnerScript:  runnerScript,
			runnerErr:     runnerErr,
			remoteSession: resolveRemoteSession(),
		}
	default:
		return &unsupportedProvider{name: providerName}
	}
}

type unsupportedProvider struct {
	name string
}

func (p *unsupportedProvider) searchTarget(query string) ([]TargetMatch, error) {
	return nil, fmt.Errorf("remote provider %q is not supported", p.name)
}

func (p *unsupportedProvider) attachTarget(localTarget string, query string) (ResolvedTarget, error) {
	return ResolvedTarget{}, fmt.Errorf("remote provider %q is not supported", p.name)
}

func (p *unsupportedProvider) ensureRemoteSession(localTarget string, remoteSession string) error {
	return fmt.Errorf("remote provider %q is not supported", p.name)
}

func (p *unsupportedProvider) reconnect(localTarget string) error {
	return fmt.Errorf("remote provider %q is not supported", p.name)
}

func (p *unsupportedProvider) detectStage(text string) model.PaneStage {
	return model.StageUnknown
}

func (c *Client) SearchTarget(query string) ([]TargetMatch, error) {
	return c.provider.searchTarget(query)
}

func (c *Client) AttachTarget(localTarget string, query string) (ResolvedTarget, error) {
	return c.provider.attachTarget(localTarget, query)
}

func (c *Client) EnsureRemoteSession(localTarget string, remoteSession string) error {
	return c.provider.ensureRemoteSession(localTarget, remoteSession)
}

func (c *Client) Reconnect(localTarget string) error {
	return c.provider.reconnect(localTarget)
}

func (c *Client) DetectStage(text string) model.PaneStage {
	return c.provider.detectStage(text)
}

func (p *jumpServerProvider) searchTarget(query string) ([]TargetMatch, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("empty host query")
	}
	if p.profilePath == "" || p.runnerScript == "" {
		return []TargetMatch{{DisplayName: query}}, nil
	}
	return []TargetMatch{{DisplayName: query}}, nil
}

func (p *jumpServerProvider) attachTarget(localTarget string, query string) (ResolvedTarget, error) {
	if err := p.validate(); err != nil {
		return ResolvedTarget{}, err
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return ResolvedTarget{}, fmt.Errorf("empty target query")
	}

	if err := p.tmux.SendCtrlC(localTarget); err != nil {
		// Ignore pre-attach interrupt failures on a fresh shell.
	}

	command := execx.ShellQuote(p.runnerScript) + " " + execx.ShellQuote(p.profilePath) + " 1"
	if err := p.tmux.SendKeys(localTarget, command); err != nil {
		return ResolvedTarget{}, fmt.Errorf("start jump profile: %w", err)
	}

	trace := []model.PaneStage{}
	snapshot, stage, err := p.waitForStage(localTarget, 45*time.Second, model.StageMenu, model.StageTargetSearch, model.StageSelection, model.StageRemoteShell, model.StageAuthPrompt)
	if err != nil {
		return ResolvedTarget{}, wrapStageWaitError(stage, trace, err)
	}
	trace = appendTrace(trace, stage)
	if stage == model.StageAuthPrompt {
		return ResolvedTarget{}, newAttachError(AttachReasonAuthPrompt, stage, trace, nil, "remote provider requires manual authentication entry")
	}

	resolvedVia := ResolvedViaDirectQuery
	if stage == model.StageMenu {
		resolvedVia = ResolvedViaTargetListSearch
		snapshot, stage, err = p.enterHostList(localTarget, stage, trace)
		if err != nil {
			return ResolvedTarget{}, err
		}
		trace = appendTrace(trace, stage)
	}

	switch stage {
	case model.StageTargetSearch:
		if err := p.tmux.SendKeys(localTarget, query); err != nil {
			return ResolvedTarget{}, fmt.Errorf("search target: %w", err)
		}
		snapshot, stage, err = p.waitForQueryResolution(localTarget, 25*time.Second)
		if err != nil {
			return ResolvedTarget{}, wrapStageWaitError(stage, trace, err)
		}
		trace = appendTrace(trace, stage)
	}

	if stage == model.StageAuthPrompt {
		return ResolvedTarget{}, newAttachError(AttachReasonAuthPrompt, stage, trace, nil, "remote provider requires manual authentication entry")
	}

	if containsNoAssets(snapshot) || stage == model.StageTargetSearch {
		return ResolvedTarget{}, newAttachError(AttachReasonQueryNoResult, stage, trace, nil, "target query returned no attachable result")
	}
	if stage == model.StageMenu {
		return ResolvedTarget{}, newAttachError(AttachReasonUnknownStage, stage, trace, nil, "remote provider returned to menu without resolving a target")
	}

	selectionID := ""
	selectionLabel := ""
	if stage == model.StageSelection {
		candidates := parseAccountCandidates(snapshot)
		if len(candidates) == 0 {
			return ResolvedTarget{}, newAttachError(AttachReasonSelectionAmbiguous, stage, trace, nil, "selection prompt was present but selectable rows could not be parsed")
		}
		selected, err := chooseAccount(candidates)
		if err != nil {
			return ResolvedTarget{}, newAttachError(AttachReasonSelectionAmbiguous, stage, trace, accountLabels(candidates), err.Error())
		}
		selectionID = selected.ID
		selectionLabel = selected.Label
		if err := p.tmux.SendKeys(localTarget, selectionID); err != nil {
			return ResolvedTarget{}, fmt.Errorf("select account: %w", err)
		}
		snapshot, stage, err = p.waitForStage(localTarget, 30*time.Second, model.StageRemoteShell, model.StageAuthPrompt)
		if err != nil {
			return ResolvedTarget{}, wrapStageWaitError(stage, trace, err)
		}
		trace = appendTrace(trace, stage)
	}

	if stage == model.StageAuthPrompt {
		return ResolvedTarget{}, newAttachError(AttachReasonAuthPrompt, stage, trace, nil, "remote provider requires manual authentication entry")
	}
	if stage != model.StageRemoteShell {
		return ResolvedTarget{}, newAttachError(AttachReasonRemoteShellNotReady, stage, trace, nil, "remote provider did not reach a remote shell")
	}

	name, address := parseAsset(snapshot)
	remoteTmux := p.ensureRemoteTmux(localTarget, p.remoteSession)

	return ResolvedTarget{
		Query:            query,
		Name:             coalesce(name, query),
		Address:          address,
		SelectionID:      selectionID,
		SelectionLabel:   selectionLabel,
		RemoteSession:    remoteTmux.Session,
		RemoteTmuxStatus: remoteTmux.Status,
		RemoteTmuxDetail: remoteTmux.Detail,
		Provider:         ProviderJumpServer,
		ResolvedVia:      resolvedVia,
		StageTrace:       trace,
	}, nil
}

func (p *jumpServerProvider) ensureRemoteSession(localTarget string, remoteSession string) error {
	outcome := p.ensureRemoteTmux(localTarget, remoteSession)
	if outcome.Status == model.RemoteTmuxStatusAttached {
		return nil
	}
	if outcome.Detail != "" {
		return errors.New(outcome.Detail)
	}
	return fmt.Errorf("remote tmux status: %s", outcome.Status)
}

func (p *jumpServerProvider) ensureRemoteTmux(localTarget string, remoteSession string) remoteTmuxOutcome {
	if remoteSession == "" {
		remoteSession = p.remoteSession
	}
	outcome := remoteTmuxOutcome{
		Session: remoteSession,
		Status:  model.RemoteTmuxStatusAttached,
	}
	marker := fmt.Sprintf("%s%d__", remoteTmuxMarkerPrefix, time.Now().UnixNano())
	command := buildRemoteTmuxAttachCommand(remoteSession, marker)
	if err := p.tmux.SendKeys(localTarget, command); err != nil {
		outcome.Status = model.RemoteTmuxStatusFailed
		outcome.Detail = fmt.Sprintf("send remote tmux attach command: %v", err)
		return outcome
	}
	return p.waitForRemoteTmuxOutcome(localTarget, remoteSession, marker, 15*time.Second)
}

func (p *jumpServerProvider) reconnect(localTarget string) error {
	return p.tmux.SendKeys(localTarget, "")
}

func DetectStage(text string) model.PaneStage {
	cleaned := sanitizeSnapshot(text)
	tail := tailLines(cleaned, 80)
	lines := strings.Split(tail, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		lowerLine := strings.ToLower(line)
		switch {
		case remotePromptRE.MatchString(line):
			return model.StageRemoteShell
		case strings.Contains(line, "[Host]>") || strings.Contains(lowerLine, "search:"):
			return model.StageTargetSearch
		case strings.Contains(line, "ID>"):
			return model.StageSelection
		case accountRowRE.MatchString(line):
			return model.StageSelection
		case strings.Contains(line, "Opt>"):
			return model.StageMenu
		case strings.Contains(line, "开始连接到") || strings.Contains(line, "Connecting to"):
			return model.StageConnecting
		case strings.Contains(lowerLine, "password") || strings.Contains(lowerLine, "verification code") || strings.Contains(lowerLine, "passcode"):
			return model.StageAuthPrompt
		}
	}
	if accountRowRE.MatchString(tail) {
		return model.StageSelection
	}
	return model.StageUnknown
}

func (p *jumpServerProvider) detectStage(text string) model.PaneStage {
	return DetectStage(text)
}

func (p *jumpServerProvider) validate() error {
	if p.profilePath == "" {
		return fmt.Errorf("jump profile not configured; set TMUX_GHOSTTY_JUMP_PROFILE")
	}
	if p.runnerErr != nil {
		return fmt.Errorf("prepare jump runner: %w", p.runnerErr)
	}
	if p.runnerScript == "" {
		return fmt.Errorf("jump runner not configured")
	}
	if _, err := os.Stat(p.profilePath); err != nil {
		return fmt.Errorf("jump profile unavailable: %w", err)
	}
	if _, err := os.Stat(p.runnerScript); err != nil {
		return fmt.Errorf("jump runner unavailable: %w", err)
	}
	return nil
}

func (p *jumpServerProvider) waitForStage(localTarget string, timeout time.Duration, expected ...model.PaneStage) (string, model.PaneStage, error) {
	deadline := time.Now().Add(timeout)
	lastText := ""
	lastStage := model.StageUnknown
	stableCount := 0
	for time.Now().Before(deadline) {
		text, err := p.tmux.CapturePane(localTarget, 220)
		if err != nil {
			return "", model.StageUnknown, err
		}
		stage := DetectStage(text)
		lastText = text
		if stage == lastStage {
			stableCount++
		} else {
			lastStage = stage
			stableCount = 1
		}
		for _, candidate := range expected {
			if stage == candidate {
				if isTransientStage(stage) && stableCount < 2 {
					break
				}
				return text, stage, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return lastText, lastStage, fmt.Errorf("timed out waiting for remote provider stage %v; last stage %s", expected, lastStage)
}

func (p *jumpServerProvider) waitForQueryResolution(localTarget string, timeout time.Duration) (string, model.PaneStage, error) {
	deadline := time.Now().Add(timeout)
	lastText := ""
	lastStage := model.StageUnknown
	for time.Now().Before(deadline) {
		text, err := p.tmux.CapturePane(localTarget, 220)
		if err != nil {
			return "", model.StageUnknown, err
		}
		stage := DetectStage(text)
		lastText = text
		lastStage = stage
		switch stage {
		case model.StageSelection, model.StageRemoteShell, model.StageAuthPrompt:
			return text, stage, nil
		case model.StageTargetSearch, model.StageMenu:
			if containsNoAssets(text) {
				return text, stage, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return lastText, lastStage, fmt.Errorf("timed out waiting for remote provider query resolution; last stage %s", lastStage)
}

func (p *jumpServerProvider) enterHostList(localTarget string, currentStage model.PaneStage, trace []model.PaneStage) (string, model.PaneStage, error) {
	if currentStage == model.StageTargetSearch {
		return "", model.StageTargetSearch, nil
	}
	if err := p.tmux.SendText(localTarget, "h"); err != nil {
		return "", model.StageUnknown, fmt.Errorf("enter host list: %w", err)
	}
	snapshot, stage, err := p.waitForHostList(localTarget, 15*time.Second)
	if err != nil {
		return "", stage, wrapStageWaitError(stage, trace, err)
	}
	if stage == model.StageAuthPrompt {
		return "", stage, newAttachError(AttachReasonAuthPrompt, stage, appendTrace(trace, stage), nil, "remote provider requires manual authentication entry")
	}
	return snapshot, stage, nil
}

func (p *jumpServerProvider) waitForHostList(localTarget string, timeout time.Duration) (string, model.PaneStage, error) {
	deadline := time.Now().Add(timeout)
	lastText := ""
	lastStage := model.StageUnknown
	confirmed := false
	for time.Now().Before(deadline) {
		text, err := p.tmux.CapturePane(localTarget, 220)
		if err != nil {
			return "", model.StageUnknown, err
		}
		stage := DetectStage(text)
		lastText = text
		lastStage = stage
		switch stage {
		case model.StageTargetSearch, model.StageAuthPrompt:
			return text, stage, nil
		}
		if !confirmed && stage == model.StageMenu && needsHostListConfirmation(text) {
			if err := p.tmux.SendKeys(localTarget, ""); err != nil {
				return "", model.StageUnknown, fmt.Errorf("confirm host list: %w", err)
			}
			confirmed = true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return lastText, lastStage, fmt.Errorf("timed out waiting for host list; last stage %s", lastStage)
}

func (p *jumpServerProvider) waitForRemoteTmuxOutcome(localTarget string, remoteSession string, marker string, timeout time.Duration) remoteTmuxOutcome {
	outcome := remoteTmuxOutcome{
		Session: remoteSession,
		Status:  model.RemoteTmuxStatusAttached,
	}
	deadline := time.Now().Add(timeout)
	markerSeenAt := time.Time{}
	for time.Now().Before(deadline) {
		text, err := p.tmux.CapturePane(localTarget, 220)
		if err != nil {
			outcome.Status = model.RemoteTmuxStatusFailed
			outcome.Detail = fmt.Sprintf("capture pane while attaching remote tmux: %v", err)
			return outcome
		}
		if status, detail, ok := parseRemoteTmuxStatus(text, marker); ok {
			outcome.Status = status
			outcome.Detail = detail
			return outcome
		}
		cleaned := sanitizeSnapshot(text)
		if markerSeenAt.IsZero() && strings.Contains(cleaned, marker) {
			markerSeenAt = time.Now()
		}
		if !markerSeenAt.IsZero() && time.Since(markerSeenAt) >= remoteTmuxOutcomeSettleFor {
			return outcome
		}
		time.Sleep(250 * time.Millisecond)
	}
	if markerSeenAt.IsZero() {
		outcome.Status = model.RemoteTmuxStatusFailed
		outcome.Detail = "timed out waiting for remote tmux attach to start"
	}
	return outcome
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
	if stage == model.StageUnknown {
		return trace
	}
	if len(trace) == 0 || trace[len(trace)-1] != stage {
		return append(trace, stage)
	}
	return trace
}

func isTransientStage(stage model.PaneStage) bool {
	switch stage {
	case model.StageMenu, model.StageConnecting, model.StageAuthPrompt:
		return true
	default:
		return false
	}
}

func wrapStageWaitError(stage model.PaneStage, trace []model.PaneStage, err error) error {
	if stage == model.StageAuthPrompt {
		return newAttachError(AttachReasonAuthPrompt, stage, appendTrace(trace, stage), nil, "remote provider requires manual authentication entry")
	}
	return newAttachError(AttachReasonStageTimeout, stage, appendTrace(trace, stage), nil, err.Error())
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

func needsHostListConfirmation(text string) bool {
	cleaned := sanitizeSnapshot(text)
	if menuPromptTypedHRE.MatchString(cleaned) {
		return true
	}
	line := lastNonEmptyLine(cleaned)
	if !strings.HasPrefix(line, "Opt>") {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "Opt>")) == "h"
}

func buildRemoteTmuxAttachCommand(remoteSession string, marker string) string {
	quotedMarker := execx.ShellQuote(marker)
	quotedSession := execx.ShellQuote(remoteSession)
	// Emit a bash-valid one-liner. Joining control keywords with "; " the
	// naive way produces sequences like "then; printf" and "fi; else" which
	// bash rejects with "syntax error near unexpected token ';'".
	return fmt.Sprintf(
		"printf '%%s\\n' %[1]s; "+
			"sleep %[2]d; "+
			"if ! command -v tmux >/dev/null 2>&1; then "+
			"printf '%%s unavailable tmux not found\\n' %[1]s; "+
			"elif tmux has-session -t %[3]s 2>/dev/null || tmux new-session -d -s %[3]s; then "+
			"if tmux attach-session -t %[3]s; then :; "+
			"else status=$?; printf '%%s failed attach-session exit=%%s\\n' %[1]s \"$status\"; fi; "+
			"else status=$?; printf '%%s failed prepare-session exit=%%s\\n' %[1]s \"$status\"; fi",
		quotedMarker,
		int(remoteTmuxProbeDelay/time.Second),
		quotedSession,
	)
}

func parseRemoteTmuxStatus(text string, marker string) (model.RemoteTmuxStatus, string, bool) {
	cleaned := sanitizeSnapshot(text)
	prefix := marker + " "
	lines := strings.Split(cleaned, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
		if len(fields) == 0 {
			continue
		}
		status := model.RemoteTmuxStatus(fields[0])
		detail := strings.TrimSpace(strings.TrimPrefix(line, prefix+fields[0]))
		switch status {
		case model.RemoteTmuxStatusUnavailable:
			if detail == "" {
				detail = "tmux is not available on the remote host"
			}
			return status, detail, true
		case model.RemoteTmuxStatusFailed:
			if detail == "" {
				detail = "remote tmux attach failed"
			}
			return status, detail, true
		}
	}

	lower := strings.ToLower(cleaned)
	switch {
	case strings.Contains(lower, "tmux: command not found"),
		strings.Contains(lower, "exec: tmux: not found"),
		strings.Contains(lower, "command not found: tmux"):
		return model.RemoteTmuxStatusUnavailable, "tmux is not available on the remote host", true
	}
	return "", "", false
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

func resolveRunnerPath() (string, error) {
	if value := os.Getenv("TMUX_GHOSTTY_JUMP_RUNNER"); value != "" {
		return value, nil
	}
	return ensureBundledRunner()
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
