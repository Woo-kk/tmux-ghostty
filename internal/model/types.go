package model

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"time"
)

const (
	StateVersion = 1
)

type WorkspaceStatus string

const (
	WorkspaceActive   WorkspaceStatus = "active"
	WorkspaceDegraded WorkspaceStatus = "degraded"
	WorkspaceClosed   WorkspaceStatus = "closed"
)

type Controller string

const (
	ControllerUser  Controller = "user"
	ControllerAgent Controller = "agent"
)

type PaneMode string

const (
	ModeIdle             PaneMode = "idle"
	ModeRunning          PaneMode = "running"
	ModeAwaitingApproval PaneMode = "awaiting_approval"
	ModeObserveOnly      PaneMode = "observe_only"
	ModeDisconnected     PaneMode = "disconnected"
)

type PaneStage string

const (
	StageUnknown      PaneStage = "unknown"
	StageShell        PaneStage = "shell"
	StageMenu         PaneStage = "menu"
	StageTargetSearch PaneStage = "target_search"
	StageSelection    PaneStage = "selection"
	StageRemoteShell  PaneStage = "remote_shell"
	StageConnecting   PaneStage = "connecting"
	StageAuthPrompt   PaneStage = "auth_prompt"
)

type RiskLevel string

const (
	RiskRead  RiskLevel = "read"
	RiskNav   RiskLevel = "nav"
	RiskRisky RiskLevel = "risky"
)

type ApprovalState string

const (
	ApprovalNotRequired ApprovalState = "not_required"
	ApprovalPending     ApprovalState = "pending"
	ApprovalApproved    ApprovalState = "approved"
	ApprovalDenied      ApprovalState = "denied"
)

type ActionStatus string

const (
	ActionQueued    ActionStatus = "queued"
	ActionSent      ActionStatus = "sent"
	ActionCompleted ActionStatus = "completed"
	ActionFailed    ActionStatus = "failed"
	ActionCancelled ActionStatus = "cancelled"
)

type Workspace struct {
	ID              string          `json:"id"`
	CreatedAt       time.Time       `json:"created_at"`
	Status          WorkspaceStatus `json:"status"`
	GhosttyWindowID string          `json:"ghostty_window_id"`
	GhosttyTabID    string          `json:"ghostty_tab_id"`
	PaneIDs         []string        `json:"pane_ids"`
}

type Pane struct {
	ID                string     `json:"id"`
	WorkspaceID       string     `json:"workspace_id"`
	RemoteProvider    string     `json:"remote_provider"`
	HostQuery         string     `json:"host_query"`
	HostResolvedName  string     `json:"host_resolved_name"`
	GhosttyTerminalID string     `json:"ghostty_terminal_id"`
	LocalTmuxSession  string     `json:"local_tmux_session"`
	LocalTmuxTarget   string     `json:"local_tmux_target"`
	OwnsLocalTmux     bool       `json:"owns_local_tmux"`
	RemoteTmuxSession string     `json:"remote_tmux_session"`
	Controller        Controller `json:"controller"`
	Mode              PaneMode   `json:"mode"`
	Stage             PaneStage  `json:"stage"`
	LastSnapshotHash  string     `json:"last_snapshot_hash"`
	LastSnapshot      string     `json:"last_snapshot"`
	LastPrompt        string     `json:"last_prompt"`
	LastExitCode      int        `json:"last_exit_code"`
	LastActivityAt    time.Time  `json:"last_activity_at"`
	LastSnapshotAt    time.Time  `json:"last_snapshot_at"`
}

type Action struct {
	ID                string        `json:"id"`
	PaneID            string        `json:"pane_id"`
	Actor             string        `json:"actor"`
	RawCommand        string        `json:"raw_command"`
	NormalizedCommand string        `json:"normalized_command"`
	Risk              RiskLevel     `json:"risk"`
	ApprovalState     ApprovalState `json:"approval_state"`
	Status            ActionStatus  `json:"status"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

type State struct {
	Version       int                  `json:"version"`
	StartedAt     time.Time            `json:"started_at"`
	LastRequestAt time.Time            `json:"last_request_at"`
	Workspaces    map[string]Workspace `json:"workspaces"`
	Panes         map[string]Pane      `json:"panes"`
}

type PaneSnapshot struct {
	PaneID         string     `json:"pane_id"`
	Text           string     `json:"text"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Mode           PaneMode   `json:"mode"`
	Stage          PaneStage  `json:"stage"`
	Controller     Controller `json:"controller"`
	Prompt         string     `json:"prompt"`
	SnapshotHash   string     `json:"snapshot_hash"`
	LocalSession   string     `json:"local_session"`
	LocalTarget    string     `json:"local_target"`
	RemoteProvider string     `json:"remote_provider"`
	RemoteSession  string     `json:"remote_session"`
}

type BrokerStatus struct {
	StartedAt          time.Time   `json:"started_at"`
	LastRequestAt      time.Time   `json:"last_request_at"`
	WorkspaceCount     int         `json:"workspace_count"`
	PaneCount          int         `json:"pane_count"`
	PendingActionCount int         `json:"pending_action_count"`
	RunningPaneCount   int         `json:"running_pane_count"`
	Workspaces         []Workspace `json:"workspaces"`
	Panes              []Pane      `json:"panes"`
}

func NewState() State {
	now := time.Now().UTC()
	return State{
		Version:       StateVersion,
		StartedAt:     now,
		LastRequestAt: now,
		Workspaces:    map[string]Workspace{},
		Panes:         map[string]Pane{},
	}
}

func NewWorkspace() Workspace {
	return Workspace{
		ID:        "ws-" + randomID(),
		CreatedAt: time.Now().UTC(),
		Status:    WorkspaceActive,
		PaneIDs:   []string{},
	}
}

func NewPane(workspaceID string) Pane {
	id := "pane-" + randomID()
	now := time.Now().UTC()
	localSession := "tg-" + id
	return Pane{
		ID:                id,
		WorkspaceID:       workspaceID,
		LocalTmuxSession:  localSession,
		LocalTmuxTarget:   localSession + ":0.0",
		OwnsLocalTmux:     true,
		RemoteTmuxSession: "tmux-ghostty",
		Controller:        ControllerUser,
		Mode:              ModeIdle,
		Stage:             StageUnknown,
		LastActivityAt:    now,
		LastSnapshotAt:    now,
	}
}

func NewAction(paneID, actor, raw, normalized string, risk RiskLevel, approval ApprovalState, status ActionStatus) Action {
	now := time.Now().UTC()
	return Action{
		ID:                "act-" + randomID(),
		PaneID:            paneID,
		Actor:             actor,
		RawCommand:        raw,
		NormalizedCommand: normalized,
		Risk:              risk,
		ApprovalState:     approval,
		Status:            status,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func SortedWorkspaces(state State) []Workspace {
	out := make([]Workspace, 0, len(state.Workspaces))
	for _, workspace := range state.Workspaces {
		out = append(out, workspace)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func SortedPanes(state State) []Pane {
	out := make([]Pane, 0, len(state.Panes))
	for _, pane := range state.Panes {
		out = append(out, pane)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WorkspaceID == out[j].WorkspaceID {
			return out[i].ID < out[j].ID
		}
		return out[i].WorkspaceID < out[j].WorkspaceID
	})
	return out
}

func SortedActions(actions []Action) []Action {
	out := append([]Action(nil), actions...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func randomID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		now := time.Now().UnixNano()
		fallback := make([]byte, 8)
		for i := range fallback {
			fallback[i] = byte(now >> (i * 8))
		}
		return hex.EncodeToString(fallback)[:8]
	}
	return hex.EncodeToString(buf)
}
