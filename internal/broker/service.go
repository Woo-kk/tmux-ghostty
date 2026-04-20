package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/control"
	"github.com/Woo-kk/tmux-ghostty/internal/execx"
	"github.com/Woo-kk/tmux-ghostty/internal/ghostty"
	"github.com/Woo-kk/tmux-ghostty/internal/logx"
	"github.com/Woo-kk/tmux-ghostty/internal/model"
	"github.com/Woo-kk/tmux-ghostty/internal/observe"
	"github.com/Woo-kk/tmux-ghostty/internal/remote"
	"github.com/Woo-kk/tmux-ghostty/internal/risk"
	"github.com/Woo-kk/tmux-ghostty/internal/rpc"
	"github.com/Woo-kk/tmux-ghostty/internal/store"
)

type GhosttyClient interface {
	RequireAvailable() error
	EnsureRunning() error
	NewWindow(initialCommand string) (ghostty.WindowRef, ghostty.TerminalRef, error)
	NewTab(windowID string, initialCommand string) (ghostty.TabRef, ghostty.TerminalRef, error)
	FocusTerminal(terminalID string) error
	ListWindows() ([]ghostty.WindowRef, error)
	ListTabs(windowID string) ([]ghostty.TabRef, error)
	ListTerminals(tabID string) ([]ghostty.TerminalRef, error)
}

type TmuxClient interface {
	HasSession(name string) (bool, error)
	ListSessions() ([]string, error)
	NewSession(name string) error
	KillSession(name string) error
	SendKeys(target string, text string) error
	SendCtrlC(target string) error
	CapturePane(target string, lines int) (string, error)
	CurrentCommand(target string) (string, error)
	TargetAlive(target string) (bool, error)
	AttachCommand(session string) string
}

type RemoteClient interface {
	SearchTarget(query string) ([]remote.TargetMatch, error)
	ConnectTarget(localTarget string) (remote.ConnectedProvider, error)
	AttachTarget(localTarget string, query string) (remote.ResolvedTarget, error)
	EnsureRemoteSession(localTarget string, remoteSession string) error
	Reconnect(localTarget string) error
	DetectStage(text string) model.PaneStage
}

const (
	observeTickInterval      = 500 * time.Millisecond
	observeGCInterval        = 30 * time.Second
	managedTmuxSessionPrefix = "tg-pane-"
)

type Service struct {
	mu           sync.Mutex
	store        store.Store
	log          *logx.Logger
	ghostty      GhosttyClient
	tmux         TmuxClient
	remote       RemoteClient
	state        model.State
	actions      []model.Action
	idleTimeout  time.Duration
	shutdown     func()
	lastObserved map[string]time.Time
	lastGCAt     time.Time
}

type WorkspaceCreateResult struct {
	Workspace model.Workspace `json:"workspace"`
	Pane      model.Pane      `json:"pane"`
}

type PreviewResult struct {
	PaneID            string          `json:"pane_id"`
	RawCommand        string          `json:"raw_command"`
	NormalizedCommand string          `json:"normalized_command"`
	Risk              model.RiskLevel `json:"risk"`
	RequiresApproval  bool            `json:"requires_approval"`
	Action            *model.Action   `json:"action,omitempty"`
}

type AttachResult struct {
	Pane   model.Pane            `json:"pane"`
	Target remote.ResolvedTarget `json:"target"`
}

type ConnectResult struct {
	Pane              model.Pane        `json:"pane"`
	Provider          string            `json:"provider"`
	Stage             model.PaneStage   `json:"stage"`
	StageTrace        []model.PaneStage `json:"stage_trace"`
	ReadyForUserInput bool              `json:"ready_for_user_input"`
}

type Empty struct{}

type workspaceCloseRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

type paneIDRequest struct {
	PaneID string `json:"pane_id"`
}

type hostAttachRequest struct {
	PaneID string `json:"pane_id"`
	Query  string `json:"query"`
}

type claimRequest struct {
	PaneID string `json:"pane_id"`
	Actor  string `json:"actor"`
}

type commandRequest struct {
	PaneID   string `json:"pane_id"`
	Actor    string `json:"actor"`
	Command  string `json:"command"`
	ActionID string `json:"action_id,omitempty"`
}

type actionRequest struct {
	ActionID string `json:"action_id"`
}

type downRequest struct {
	Force bool `json:"force"`
}

func NewService(statePath string, actionsPath string, idleTimeout time.Duration, log *logx.Logger, ghosttyClient GhosttyClient, tmuxClient TmuxClient, remoteClient RemoteClient) (*Service, error) {
	stateStore := store.New(statePath, actionsPath)
	state, err := stateStore.LoadState()
	if err != nil {
		return nil, err
	}
	actions, err := stateStore.LoadActions()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	state.StartedAt = now
	state.LastRequestAt = now
	service := &Service{
		store:        stateStore,
		log:          log,
		ghostty:      ghosttyClient,
		tmux:         tmuxClient,
		remote:       remoteClient,
		state:        state,
		actions:      actions,
		idleTimeout:  idleTimeout,
		lastObserved: map[string]time.Time{},
	}
	service.syncStatusLocked()
	service.gcLocked(now)
	if err := service.saveLocked(); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) SetShutdownFunc(fn func()) {
	s.shutdown = fn
}

func (s *Service) Start(ctx context.Context) {
	go s.observeLoop(ctx)
}

func (s *Service) HandleRPC(ctx context.Context, method string, params json.RawMessage) (any, *rpc.RPCError) {
	var (
		result any
		err    error
	)
	switch method {
	case "broker.status":
		result, err = s.Status()
	case "broker.shutdown":
		var req downRequest
		if err = decodeParams(params, &req); err == nil {
			err = s.Shutdown(req.Force)
			result = Empty{}
		}
	case "workspace.create":
		result, err = s.CreateWorkspace()
	case "workspace.reconcile":
		result, err = s.Reconcile()
	case "workspace.close":
		var req workspaceCloseRequest
		if err = decodeParams(params, &req); err == nil {
			err = s.CloseWorkspace(req.WorkspaceID)
			result = Empty{}
		}
	case "pane.list":
		result, err = s.ListPanes()
	case "pane.focus":
		var req paneIDRequest
		if err = decodeParams(params, &req); err == nil {
			err = s.FocusPane(req.PaneID)
			result = Empty{}
		}
	case "pane.snapshot":
		var req paneIDRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.SnapshotPane(req.PaneID)
		}
	case "host.connect":
		var req paneIDRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.ConnectHost(req.PaneID)
		}
	case "host.attach":
		var req hostAttachRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.AttachHost(req.PaneID, req.Query)
		}
	case "control.claim":
		var req claimRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.Claim(req.PaneID, req.Actor)
		}
	case "control.release":
		var req paneIDRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.Release(req.PaneID)
		}
	case "control.observe":
		var req paneIDRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.Observe(req.PaneID)
		}
	case "command.preview":
		var req commandRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.PreviewCommand(req.PaneID, req.Actor, req.Command)
		}
	case "command.send":
		var req commandRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.SendCommand(req.PaneID, req.Actor, req.Command, req.ActionID)
		}
	case "command.interrupt":
		var req paneIDRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.InterruptPane(req.PaneID)
		}
	case "command.approve":
		var req actionRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.Approve(req.ActionID)
		}
	case "command.deny":
		var req actionRequest
		if err = decodeParams(params, &req); err == nil {
			result, err = s.Deny(req.ActionID)
		}
	case "actions.list":
		result, err = s.ListActions()
	default:
		err = newError(rpc.ReasonInvalidState, fmt.Errorf("unknown method: %s", method))
	}
	return result, toRPCError(err)
}

func (s *Service) Status() (model.BrokerStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()
	s.syncStatusLocked()
	s.gcLocked(time.Now().UTC())
	return s.statusLocked(), s.saveLocked()
}

func (s *Service) CreateWorkspace() (WorkspaceCreateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	if err := s.ghostty.EnsureRunning(); err != nil {
		return WorkspaceCreateResult{}, newError(rpc.ReasonGhosttyUnavailable, err)
	}

	workspace := model.NewWorkspace()
	pane := model.NewPane(workspace.ID)
	pane.OwnsLocalTmux = true
	if err := s.tmux.NewSession(pane.LocalTmuxSession); err != nil {
		return WorkspaceCreateResult{}, newError(rpc.ReasonTmuxUnavailable, err)
	}

	windowRef, terminalRef, err := s.ghostty.NewWindow(s.launchCommandForPane(pane))
	if err != nil {
		_ = s.tmux.KillSession(pane.LocalTmuxSession)
		return WorkspaceCreateResult{}, newError(rpc.ReasonGhosttyUnavailable, err)
	}

	workspace.GhosttyWindowID = windowRef.ID
	workspace.GhosttyTabID = windowRef.SelectedTabID
	workspace.PaneIDs = []string{pane.ID}
	pane.GhosttyTerminalID = terminalRef.ID

	s.state.Workspaces[workspace.ID] = workspace
	s.state.Panes[pane.ID] = pane
	if _, err := s.refreshPaneLocked(pane.ID); err != nil {
		return WorkspaceCreateResult{}, err
	}
	if err := s.saveLocked(); err != nil {
		return WorkspaceCreateResult{}, err
	}
	return WorkspaceCreateResult{Workspace: workspace, Pane: s.state.Panes[pane.ID]}, nil
}

func (s *Service) Reconcile() ([]model.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	rebuildIDs, err := s.planReconcileLocked()
	if err != nil {
		return nil, err
	}
	if len(rebuildIDs) > 0 {
		if err := s.ghostty.EnsureRunning(); err != nil {
			return nil, newError(rpc.ReasonGhosttyUnavailable, err)
		}
		for _, workspaceID := range rebuildIDs {
			updated, err := s.rebuildWorkspaceLocked(workspaceID)
			if err != nil {
				return nil, err
			}
			s.state.Workspaces[workspaceID] = updated
		}
	}
	s.gcLocked(time.Now().UTC())

	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return model.SortedWorkspaces(s.state), nil
}

func (s *Service) CloseWorkspace(workspaceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	workspace, ok := s.state.Workspaces[workspaceID]
	if !ok {
		return newError(rpc.ReasonInvalidState, fmt.Errorf("workspace not found: %s", workspaceID))
	}
	for _, paneID := range workspace.PaneIDs {
		pane, ok := s.state.Panes[paneID]
		if !ok {
			continue
		}
		if pane.OwnsLocalTmux {
			_ = s.tmux.KillSession(pane.LocalTmuxSession)
		}
		pane.Mode = model.ModeDisconnected
		pane.GhosttyTerminalID = ""
		pane.Stage = model.StageUnknown
		s.state.Panes[paneID] = pane
	}
	workspace.Status = model.WorkspaceClosed
	workspace.GhosttyWindowID = ""
	workspace.GhosttyTabID = ""
	s.state.Workspaces[workspace.ID] = workspace
	s.gcLocked(time.Now().UTC())
	return s.saveLocked()
}

func (s *Service) ListPanes() ([]model.Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()
	panes := model.SortedPanes(s.state)
	return panes, s.saveLocked()
}

func (s *Service) FocusPane(paneID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	pane, err := s.paneLocked(paneID)
	if err != nil {
		return err
	}
	if pane.GhosttyTerminalID == "" {
		return newError(rpc.ReasonInvalidState, fmt.Errorf("pane %s has no ghostty terminal", paneID))
	}
	if err := s.ghostty.FocusTerminal(pane.GhosttyTerminalID); err != nil {
		return newError(rpc.ReasonGhosttyUnavailable, err)
	}
	return s.saveLocked()
}

func (s *Service) SnapshotPane(paneID string) (model.PaneSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	pane, err := s.refreshPaneLocked(paneID)
	if err != nil {
		return model.PaneSnapshot{}, err
	}
	if err := s.saveLocked(); err != nil {
		return model.PaneSnapshot{}, err
	}
	return toSnapshot(pane), nil
}

func (s *Service) ConnectHost(paneID string) (ConnectResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	pane, err := s.paneLocked(paneID)
	if err != nil {
		return ConnectResult{}, err
	}
	connected, err := s.remote.ConnectTarget(pane.LocalTmuxTarget)
	if err != nil {
		return ConnectResult{}, newError(rpc.ReasonRemoteAttachFailed, err)
	}
	pane.RemoteProvider = connected.Provider
	pane.HostQuery = ""
	pane.HostResolvedName = ""
	pane.RemoteTmuxSession = ""
	pane.RemoteTmuxStatus = ""
	pane.RemoteTmuxDetail = ""
	pane.Mode = model.ModeRunning
	s.state.Panes[pane.ID] = pane
	pane, err = s.refreshPaneLocked(pane.ID)
	if err != nil {
		return ConnectResult{}, err
	}
	if err := s.saveLocked(); err != nil {
		return ConnectResult{}, err
	}
	return ConnectResult{
		Pane:              pane,
		Provider:          connected.Provider,
		Stage:             connected.Stage,
		StageTrace:        connected.StageTrace,
		ReadyForUserInput: connected.ReadyForUserInput,
	}, nil
}

func (s *Service) AttachHost(paneID string, query string) (AttachResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	pane, err := s.paneLocked(paneID)
	if err != nil {
		return AttachResult{}, err
	}
	resolved, err := s.remote.AttachTarget(pane.LocalTmuxTarget, query)
	if err != nil {
		return AttachResult{}, newError(rpc.ReasonRemoteAttachFailed, err)
	}
	pane.RemoteProvider = resolved.Provider
	pane.HostQuery = query
	pane.HostResolvedName = coalesce(resolved.Name, query)
	pane.RemoteTmuxSession = resolved.RemoteSession
	pane.RemoteTmuxStatus = resolved.RemoteTmuxStatus
	pane.RemoteTmuxDetail = resolved.RemoteTmuxDetail
	pane.Mode = model.ModeRunning
	s.state.Panes[pane.ID] = pane
	pane, err = s.refreshPaneLocked(pane.ID)
	if err != nil {
		return AttachResult{}, err
	}
	if err := s.saveLocked(); err != nil {
		return AttachResult{}, err
	}
	return AttachResult{Pane: pane, Target: resolved}, nil
}

func (s *Service) Claim(paneID string, actor string) (model.Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	pane, err := s.paneLocked(paneID)
	if err != nil {
		return model.Pane{}, err
	}
	controller := model.Controller(strings.ToLower(strings.TrimSpace(actor)))
	if controller != model.ControllerAgent && controller != model.ControllerUser {
		return model.Pane{}, newError(rpc.ReasonInvalidState, fmt.Errorf("unsupported actor: %s", actor))
	}
	pane = control.Claim(pane, controller)
	s.state.Panes[pane.ID] = pane
	return pane, s.saveLocked()
}

func (s *Service) Release(paneID string) (model.Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	pane, err := s.paneLocked(paneID)
	if err != nil {
		return model.Pane{}, err
	}
	pane = control.Release(pane)
	s.state.Panes[pane.ID] = pane
	return pane, s.saveLocked()
}

func (s *Service) Observe(paneID string) (model.Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	pane, err := s.paneLocked(paneID)
	if err != nil {
		return model.Pane{}, err
	}
	pane = control.Observe(pane)
	s.state.Panes[pane.ID] = pane
	return pane, s.saveLocked()
}

func (s *Service) PreviewCommand(paneID string, actor string, command string) (PreviewResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	pane, err := s.refreshPaneLocked(paneID)
	if err != nil {
		return PreviewResult{}, err
	}
	if err := control.RequireAgentControl(pane); err != nil {
		return PreviewResult{}, newError(rpc.ReasonNotController, err)
	}
	if pending := s.pendingActionForPaneLocked(paneID); pending != nil {
		return PreviewResult{}, newError(rpc.ReasonInvalidState, fmt.Errorf("pane %s already has a pending approval action", paneID))
	}

	normalized, riskLevel := risk.Classify(command, risk.Context{Stage: pane.Stage})
	result := PreviewResult{
		PaneID:            paneID,
		RawCommand:        command,
		NormalizedCommand: normalized,
		Risk:              riskLevel,
		RequiresApproval:  riskLevel == model.RiskRisky,
	}
	if riskLevel == model.RiskRisky {
		action := model.NewAction(paneID, actor, strings.TrimSpace(command), normalized, riskLevel, model.ApprovalPending, model.ActionQueued)
		s.actions = append(s.actions, action)
		pane.Mode = model.ModeAwaitingApproval
		s.state.Panes[pane.ID] = pane
		result.Action = &action
	}
	return result, s.saveLocked()
}

func (s *Service) SendCommand(paneID string, actor string, command string, actionID string) (model.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()
	return s.sendCommandLocked(paneID, actor, command, actionID)
}

func (s *Service) InterruptPane(paneID string) (model.Pane, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	pane, err := s.paneLocked(paneID)
	if err != nil {
		return model.Pane{}, err
	}
	if err := s.tmux.SendCtrlC(pane.LocalTmuxTarget); err != nil {
		return model.Pane{}, newError(rpc.ReasonTmuxUnavailable, err)
	}
	now := time.Now().UTC()
	for index := len(s.actions) - 1; index >= 0; index-- {
		if s.actions[index].PaneID == pane.ID && s.actions[index].Status == model.ActionSent {
			s.actions[index].Status = model.ActionCancelled
			s.actions[index].UpdatedAt = now
			break
		}
	}
	if pane.Mode != model.ModeDisconnected {
		pane.Mode = model.ModeIdle
	}
	s.state.Panes[pane.ID] = pane
	return pane, s.saveLocked()
}

func (s *Service) Approve(actionID string) (model.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	index, action, err := s.actionLocked(actionID)
	if err != nil {
		return model.Action{}, err
	}
	if action.ApprovalState != model.ApprovalPending {
		return model.Action{}, newError(rpc.ReasonInvalidState, fmt.Errorf("action %s is not pending approval", actionID))
	}
	action.ApprovalState = model.ApprovalApproved
	action.UpdatedAt = time.Now().UTC()
	s.actions[index] = action
	return s.sendCommandLocked(action.PaneID, action.Actor, action.RawCommand, action.ID)
}

func (s *Service) Deny(actionID string) (model.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	index, action, err := s.actionLocked(actionID)
	if err != nil {
		return model.Action{}, err
	}
	if action.ApprovalState != model.ApprovalPending {
		return model.Action{}, newError(rpc.ReasonInvalidState, fmt.Errorf("action %s is not pending approval", actionID))
	}
	action.ApprovalState = model.ApprovalDenied
	action.Status = model.ActionCancelled
	action.UpdatedAt = time.Now().UTC()
	s.actions[index] = action
	pane := s.state.Panes[action.PaneID]
	if pane.Mode == model.ModeAwaitingApproval {
		pane.Mode = model.ModeIdle
		s.state.Panes[pane.ID] = pane
	}
	return action, s.saveLocked()
}

func (s *Service) ListActions() ([]model.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()
	return model.SortedActions(s.actions), s.saveLocked()
}

func (s *Service) Shutdown(force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked()

	if !force {
		for _, workspace := range s.state.Workspaces {
			if workspace.Status != model.WorkspaceClosed {
				return newError(rpc.ReasonInvalidState, fmt.Errorf("active workspace exists: %s", workspace.ID))
			}
		}
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	if s.shutdown != nil {
		go s.shutdown()
	}
	return nil
}

func (s *Service) observeLoop(ctx context.Context) {
	ticker := time.NewTicker(observeTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.pollOnce(now)
		}
	}
}

func (s *Service) pollOnce(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for _, pane := range model.SortedPanes(s.state) {
		interval := 2 * time.Second
		if pane.Mode == model.ModeRunning {
			interval = 500 * time.Millisecond
		}
		if pane.Mode == model.ModeAwaitingApproval {
			interval = 2 * time.Second
		}
		last, ok := s.lastObserved[pane.ID]
		if ok && now.Sub(last) < interval {
			continue
		}
		s.lastObserved[pane.ID] = now
		before := s.state.Panes[pane.ID]
		if _, err := s.refreshPaneLocked(pane.ID); err != nil {
			if s.log != nil {
				s.log.Error("broker.observe.refresh_failed", map[string]any{
					"pane_id": pane.ID,
					"error":   err.Error(),
				})
			}
			continue
		}
		after := s.state.Panes[pane.ID]
		if before != after {
			changed = true
		}
	}

	if s.lastGCAt.IsZero() || now.Sub(s.lastGCAt) >= observeGCInterval {
		if s.gcLocked(now) {
			changed = true
		}
	}
	if changed {
		_ = s.saveLocked()
	}
	if s.shouldAutoExitLocked(now) && s.shutdown != nil {
		go s.shutdown()
	}
}

func (s *Service) gcLocked(now time.Time) bool {
	prunedPanes := map[string]struct{}{}
	prunedWorkspaces := map[string]struct{}{}

	for workspaceID, workspace := range s.state.Workspaces {
		if workspace.Status == model.WorkspaceClosed || len(workspace.PaneIDs) == 0 {
			prunedWorkspaces[workspaceID] = struct{}{}
		}
	}
	for paneID, pane := range s.state.Panes {
		workspace, ok := s.state.Workspaces[pane.WorkspaceID]
		if !ok {
			prunedPanes[paneID] = struct{}{}
			continue
		}
		if _, prune := prunedWorkspaces[workspace.ID]; prune {
			prunedPanes[paneID] = struct{}{}
			continue
		}
		if s.paneReclaimableLocked(pane) {
			prunedPanes[paneID] = struct{}{}
		}
	}

	changed := false
	if len(prunedPanes) > 0 {
		for paneID := range prunedPanes {
			pane, ok := s.state.Panes[paneID]
			if !ok {
				continue
			}
			if pane.OwnsLocalTmux {
				s.killSessionBestEffortLocked(pane.LocalTmuxSession, "broker.gc.pruned_pane_session", paneID)
			}
			delete(s.state.Panes, paneID)
			delete(s.lastObserved, paneID)
			changed = true
		}
	}

	for workspaceID, workspace := range s.state.Workspaces {
		if _, prune := prunedWorkspaces[workspaceID]; prune {
			delete(s.state.Workspaces, workspaceID)
			changed = true
			continue
		}
		filteredPaneIDs := make([]string, 0, len(workspace.PaneIDs))
		for _, paneID := range workspace.PaneIDs {
			if _, ok := s.state.Panes[paneID]; ok {
				filteredPaneIDs = append(filteredPaneIDs, paneID)
			}
		}
		if len(filteredPaneIDs) == 0 {
			delete(s.state.Workspaces, workspaceID)
			changed = true
			continue
		}
		if !slices.Equal(filteredPaneIDs, workspace.PaneIDs) {
			workspace.PaneIDs = filteredPaneIDs
			s.state.Workspaces[workspaceID] = workspace
			changed = true
		}
	}

	remainingPaneIDs := make(map[string]struct{}, len(s.state.Panes))
	remainingManagedSessions := map[string]struct{}{}
	for paneID, pane := range s.state.Panes {
		remainingPaneIDs[paneID] = struct{}{}
		if strings.HasPrefix(strings.TrimSpace(pane.LocalTmuxSession), managedTmuxSessionPrefix) {
			remainingManagedSessions[pane.LocalTmuxSession] = struct{}{}
		}
	}
	for paneID := range s.lastObserved {
		if _, ok := remainingPaneIDs[paneID]; ok {
			continue
		}
		delete(s.lastObserved, paneID)
		changed = true
	}
	if len(s.actions) > 0 {
		filteredActions := s.actions[:0]
		for _, action := range s.actions {
			if _, ok := remainingPaneIDs[action.PaneID]; ok {
				filteredActions = append(filteredActions, action)
				continue
			}
			changed = true
		}
		s.actions = filteredActions
	}
	for workspaceID := range s.state.Workspaces {
		s.updateWorkspaceStatusLocked(workspaceID)
	}

	s.killOrphanManagedSessionsLocked(remainingManagedSessions)
	s.lastGCAt = now
	return changed
}

func (s *Service) paneReclaimableLocked(pane model.Pane) bool {
	target := strings.TrimSpace(pane.LocalTmuxTarget)
	if target != "" {
		alive, err := s.tmux.TargetAlive(target)
		if err != nil {
			if s.log != nil {
				s.log.Error("broker.gc.target_alive_check_failed", map[string]any{
					"pane_id": pane.ID,
					"target":  target,
					"error":   err.Error(),
				})
			}
			return false
		}
		if alive {
			return false
		}
	}

	session := strings.TrimSpace(pane.LocalTmuxSession)
	if session == "" {
		return true
	}
	alive, err := s.tmux.HasSession(session)
	if err != nil {
		if s.log != nil {
			s.log.Error("broker.gc.session_alive_check_failed", map[string]any{
				"pane_id": pane.ID,
				"session": session,
				"error":   err.Error(),
			})
		}
		return false
	}
	return !alive
}

func (s *Service) killSessionBestEffortLocked(session string, msg string, paneID string) {
	session = strings.TrimSpace(session)
	if session == "" {
		return
	}
	if err := s.tmux.KillSession(session); err != nil && s.log != nil {
		s.log.Error(msg, map[string]any{
			"pane_id": paneID,
			"session": session,
			"error":   err.Error(),
		})
	}
}

func (s *Service) killOrphanManagedSessionsLocked(referenced map[string]struct{}) {
	sessions, err := s.tmux.ListSessions()
	if err != nil {
		if s.log != nil {
			s.log.Error("broker.gc.list_sessions_failed", map[string]any{
				"error": err.Error(),
			})
		}
		return
	}
	seen := map[string]struct{}{}
	for _, session := range sessions {
		session = strings.TrimSpace(session)
		if session == "" || !strings.HasPrefix(session, managedTmuxSessionPrefix) {
			continue
		}
		if _, ok := referenced[session]; ok {
			continue
		}
		if _, ok := seen[session]; ok {
			continue
		}
		seen[session] = struct{}{}
		s.killSessionBestEffortLocked(session, "broker.gc.orphan_session_kill_failed", "")
	}
}

func (s *Service) sendCommandLocked(paneID string, actor string, command string, actionID string) (model.Action, error) {
	pane, err := s.refreshPaneLocked(paneID)
	if err != nil {
		return model.Action{}, err
	}
	if err := control.RequireAgentControl(pane); err != nil {
		return model.Action{}, newError(rpc.ReasonNotController, err)
	}
	if pending := s.pendingActionForPaneLocked(paneID); pending != nil && pending.ID != actionID {
		return model.Action{}, newError(rpc.ReasonApprovalRequired, fmt.Errorf("pane %s has a pending approval action", paneID))
	}

	rawCommand := strings.TrimSpace(command)
	normalized, riskLevel := risk.Classify(rawCommand, risk.Context{Stage: pane.Stage})
	now := time.Now().UTC()

	var action model.Action
	if riskLevel == model.RiskRisky {
		if actionID == "" {
			return model.Action{}, newError(rpc.ReasonApprovalRequired, fmt.Errorf("risky command requires approval"))
		}
		index, existing, err := s.actionLocked(actionID)
		if err != nil {
			return model.Action{}, err
		}
		if existing.ApprovalState != model.ApprovalApproved {
			return model.Action{}, newError(rpc.ReasonApprovalRequired, fmt.Errorf("action %s is not approved", actionID))
		}
		action = existing
		action.Status = model.ActionSent
		action.UpdatedAt = now
		s.actions[index] = action
		rawCommand = strings.TrimSpace(action.RawCommand)
	} else {
		action = model.NewAction(pane.ID, actor, rawCommand, normalized, riskLevel, model.ApprovalNotRequired, model.ActionSent)
		s.actions = append(s.actions, action)
	}

	if err := s.tmux.SendKeys(pane.LocalTmuxTarget, rawCommand); err != nil {
		action.Status = model.ActionFailed
		action.UpdatedAt = time.Now().UTC()
		s.replaceActionLocked(action)
		return model.Action{}, newError(rpc.ReasonTmuxUnavailable, err)
	}

	pane.Mode = model.ModeRunning
	pane.LastActivityAt = time.Now().UTC()
	s.state.Panes[pane.ID] = pane
	if err := s.saveLocked(); err != nil {
		return model.Action{}, err
	}
	return action, nil
}

func (s *Service) loadGhosttyTopologyLocked() (map[string]bool, map[string]bool, map[string]bool, error) {
	existingTerminals := map[string]bool{}
	existingTabs := map[string]bool{}
	existingWindows := map[string]bool{}

	windows, err := s.ghostty.ListWindows()
	if err != nil {
		return nil, nil, nil, newError(rpc.ReasonGhosttyUnavailable, err)
	}
	for _, window := range windows {
		existingWindows[window.ID] = true
		tabs, err := s.ghostty.ListTabs(window.ID)
		if err != nil {
			return nil, nil, nil, newError(rpc.ReasonGhosttyUnavailable, err)
		}
		for _, tab := range tabs {
			existingTabs[tab.ID] = true
			terminals, err := s.ghostty.ListTerminals(tab.ID)
			if err != nil {
				return nil, nil, nil, newError(rpc.ReasonGhosttyUnavailable, err)
			}
			for _, terminal := range terminals {
				existingTerminals[terminal.ID] = true
			}
		}
	}
	return existingTerminals, existingTabs, existingWindows, nil
}

func (s *Service) rebuildWorkspaceLocked(workspaceID string) (model.Workspace, error) {
	workspace, ok := s.state.Workspaces[workspaceID]
	if !ok {
		return model.Workspace{}, newError(rpc.ReasonInvalidState, fmt.Errorf("workspace not found: %s", workspaceID))
	}
	if len(workspace.PaneIDs) == 0 {
		return workspace, nil
	}

	firstPane := s.state.Panes[workspace.PaneIDs[0]]
	if alive, _ := s.tmux.TargetAlive(firstPane.LocalTmuxTarget); !alive {
		if firstPane.OwnsLocalTmux {
			if err := s.tmux.NewSession(firstPane.LocalTmuxSession); err != nil {
				return model.Workspace{}, newError(rpc.ReasonTmuxUnavailable, err)
			}
		} else if sessionAlive, err := s.tmux.HasSession(firstPane.LocalTmuxSession); err != nil {
			return model.Workspace{}, newError(rpc.ReasonTmuxUnavailable, err)
		} else if !sessionAlive {
			return model.Workspace{}, newError(rpc.ReasonInvalidState, fmt.Errorf("pane %s no longer has a live tmux session", firstPane.ID))
		}
		firstPane.LocalTmuxTarget = firstPane.LocalTmuxSession + ":0.0"
	}

	windowRef, terminalRef, err := s.ghostty.NewWindow(s.launchCommandForPane(firstPane))
	if err != nil {
		return model.Workspace{}, newError(rpc.ReasonGhosttyUnavailable, err)
	}
	workspace.GhosttyWindowID = windowRef.ID
	workspace.GhosttyTabID = windowRef.SelectedTabID
	firstPane.GhosttyTerminalID = terminalRef.ID
	firstPane.Mode = model.ModeIdle
	s.state.Panes[firstPane.ID] = firstPane
	workspace.Status = model.WorkspaceActive
	s.state.Workspaces[workspace.ID] = workspace
	return workspace, nil
}

func (s *Service) paneLocked(paneID string) (model.Pane, error) {
	pane, ok := s.state.Panes[paneID]
	if !ok {
		return model.Pane{}, newError(rpc.ReasonPaneNotFound, fmt.Errorf("pane not found: %s", paneID))
	}
	return pane, nil
}

func (s *Service) actionLocked(actionID string) (int, model.Action, error) {
	for index, action := range s.actions {
		if action.ID == actionID {
			return index, action, nil
		}
	}
	return -1, model.Action{}, newError(rpc.ReasonInvalidState, fmt.Errorf("action not found: %s", actionID))
}

func (s *Service) pendingActionForPaneLocked(paneID string) *model.Action {
	for index := len(s.actions) - 1; index >= 0; index-- {
		action := s.actions[index]
		if action.PaneID == paneID && action.ApprovalState == model.ApprovalPending {
			return &action
		}
	}
	return nil
}

func (s *Service) replaceActionLocked(updated model.Action) {
	for index := range s.actions {
		if s.actions[index].ID == updated.ID {
			s.actions[index] = updated
			return
		}
	}
	s.actions = append(s.actions, updated)
}

func (s *Service) refreshPaneLocked(paneID string) (model.Pane, error) {
	pane, err := s.paneLocked(paneID)
	if err != nil {
		return model.Pane{}, err
	}

	alive, err := s.tmux.TargetAlive(pane.LocalTmuxTarget)
	if err != nil {
		return model.Pane{}, newError(rpc.ReasonTmuxUnavailable, err)
	}
	if !alive {
		pane.Mode = model.ModeDisconnected
		pane.Stage = model.StageUnknown
		s.state.Panes[pane.ID] = pane
		s.updateWorkspaceStatusLocked(pane.WorkspaceID)
		return pane, nil
	}

	text, err := s.tmux.CapturePane(pane.LocalTmuxTarget, 500)
	if err != nil {
		return model.Pane{}, newError(rpc.ReasonTmuxUnavailable, err)
	}
	now := time.Now().UTC()
	hash := observe.HashText(text)
	if hash != pane.LastSnapshotHash {
		pane.LastSnapshot = text
		pane.LastSnapshotHash = hash
		pane.LastSnapshotAt = now
		pane.LastActivityAt = now
	}

	prompt := observe.ExtractPrompt(text)
	currentCommand := ""
	if prompt != "" {
		pane.LastPrompt = prompt
		switch pane.Mode {
		case model.ModeRunning, model.ModeObserveOnly, model.ModeDisconnected:
			pane.Mode = model.ModeIdle
			s.completeLatestActionLocked(pane.ID, model.ActionCompleted)
		}
	} else if pane.Mode != model.ModeAwaitingApproval {
		currentCommand, err = s.tmux.CurrentCommand(pane.LocalTmuxTarget)
		if err == nil {
			switch {
			case observe.IsInteractiveCommand(currentCommand):
				pane.Mode = model.ModeObserveOnly
			case observe.IsShellLikeCommand(currentCommand):
				if pane.Mode != model.ModeRunning {
					pane.Mode = model.ModeIdle
				}
			case strings.TrimSpace(currentCommand) != "":
				pane.Mode = model.ModeRunning
			}
		}
	}
	pane.Stage = inferPaneStage(pane, text, currentCommand)

	s.state.Panes[pane.ID] = pane
	s.updateWorkspaceStatusLocked(pane.WorkspaceID)
	return pane, nil
}

func (s *Service) completeLatestActionLocked(paneID string, status model.ActionStatus) {
	now := time.Now().UTC()
	for index := len(s.actions) - 1; index >= 0; index-- {
		if s.actions[index].PaneID == paneID && s.actions[index].Status == model.ActionSent {
			s.actions[index].Status = status
			s.actions[index].UpdatedAt = now
			return
		}
	}
}

func (s *Service) updateWorkspaceStatusLocked(workspaceID string) {
	workspace, ok := s.state.Workspaces[workspaceID]
	if !ok || workspace.Status == model.WorkspaceClosed {
		return
	}
	status := model.WorkspaceActive
	if strings.TrimSpace(workspace.GhosttyWindowID) == "" || strings.TrimSpace(workspace.GhosttyTabID) == "" {
		status = model.WorkspaceDegraded
	}
	for _, paneID := range workspace.PaneIDs {
		pane, ok := s.state.Panes[paneID]
		if !ok || pane.Mode == model.ModeDisconnected {
			status = model.WorkspaceDegraded
			break
		}
	}
	workspace.Status = status
	s.state.Workspaces[workspaceID] = workspace
}

func (s *Service) syncStatusLocked() {
	if err := s.syncGhosttyTopologyLocked(); err != nil && s.log != nil {
		s.log.Error("broker.status.topology_sync_failed", map[string]any{
			"error": err.Error(),
		})
	}
	for _, pane := range model.SortedPanes(s.state) {
		if strings.TrimSpace(pane.GhosttyTerminalID) == "" {
			continue
		}
		if _, err := s.refreshPaneLocked(pane.ID); err != nil && s.log != nil {
			s.log.Error("broker.status.refresh_failed", map[string]any{
				"pane_id": pane.ID,
				"error":   err.Error(),
			})
		}
	}
}

func (s *Service) planReconcileLocked() ([]string, error) {
	rebuildIDs := []string{}
	if err := s.syncGhosttyTopologyLocked(); err != nil {
		return nil, err
	}
	for _, workspace := range model.SortedWorkspaces(s.state) {
		if workspace.Status == model.WorkspaceClosed {
			continue
		}
		if s.workspaceHealthyLocked(workspace) {
			continue
		}
		if s.workspaceAllowsRebuildLocked(workspace) {
			rebuildIDs = append(rebuildIDs, workspace.ID)
		}
	}
	return rebuildIDs, nil
}

func (s *Service) syncGhosttyTopologyLocked() error {
	if err := s.ghostty.RequireAvailable(); err != nil {
		for _, workspace := range model.SortedWorkspaces(s.state) {
			if workspace.Status == model.WorkspaceClosed {
				continue
			}
			if workspace.LaunchMode == "" {
				workspace.LaunchMode = s.workspaceLaunchModeLocked(workspace)
			}
			workspace.GhosttyWindowID = ""
			workspace.GhosttyTabID = ""
			s.state.Workspaces[workspace.ID] = workspace
			for _, paneID := range workspace.PaneIDs {
				pane, ok := s.state.Panes[paneID]
				if !ok {
					continue
				}
				pane.GhosttyTerminalID = ""
				pane.Mode = model.ModeDisconnected
				pane.Stage = model.StageUnknown
				s.state.Panes[pane.ID] = pane
			}
			s.updateWorkspaceStatusLocked(workspace.ID)
		}
		return nil
	}
	existingTerminals, existingTabs, existingWindows, err := s.loadGhosttyTopologyLocked()
	if err != nil {
		return err
	}
	for _, workspace := range model.SortedWorkspaces(s.state) {
		if workspace.Status == model.WorkspaceClosed {
			continue
		}
		if workspace.LaunchMode == "" {
			workspace.LaunchMode = s.workspaceLaunchModeLocked(workspace)
		}
		if workspace.GhosttyWindowID != "" && !existingWindows[workspace.GhosttyWindowID] {
			workspace.GhosttyWindowID = ""
		}
		if workspace.GhosttyTabID != "" && !existingTabs[workspace.GhosttyTabID] {
			workspace.GhosttyTabID = ""
		}
		s.state.Workspaces[workspace.ID] = workspace
		for _, paneID := range workspace.PaneIDs {
			pane, ok := s.state.Panes[paneID]
			if !ok {
				continue
			}
			if pane.GhosttyTerminalID != "" && !existingTerminals[pane.GhosttyTerminalID] {
				pane.GhosttyTerminalID = ""
				pane.Mode = model.ModeDisconnected
				pane.Stage = model.StageUnknown
				s.state.Panes[pane.ID] = pane
			}
		}
		s.updateWorkspaceStatusLocked(workspace.ID)
	}
	return nil
}

func (s *Service) workspaceHealthyLocked(workspace model.Workspace) bool {
	if strings.TrimSpace(workspace.GhosttyWindowID) == "" || strings.TrimSpace(workspace.GhosttyTabID) == "" {
		return false
	}
	if len(workspace.PaneIDs) == 0 {
		return true
	}
	for _, paneID := range workspace.PaneIDs {
		pane, ok := s.state.Panes[paneID]
		if !ok || strings.TrimSpace(pane.GhosttyTerminalID) == "" || pane.Mode == model.ModeDisconnected {
			return false
		}
	}
	return true
}

func (s *Service) workspaceAllowsRebuildLocked(workspace model.Workspace) bool {
	return s.workspaceLaunchModeLocked(workspace) == model.WorkspaceLaunchModeNewWindow && len(workspace.PaneIDs) <= 1
}

func (s *Service) workspaceLaunchModeLocked(workspace model.Workspace) model.WorkspaceLaunchMode {
	if workspace.LaunchMode != "" {
		return workspace.LaunchMode
	}
	if len(workspace.PaneIDs) > 0 {
		if pane, ok := s.state.Panes[workspace.PaneIDs[0]]; ok && !pane.OwnsLocalTmux {
			return model.WorkspaceLaunchModeCurrentWindow
		}
	}
	return model.WorkspaceLaunchModeNewWindow
}

func inferPaneStage(pane model.Pane, text string, currentCommand string) model.PaneStage {
	stage := remote.DetectStage(text)
	switch stage {
	case model.StageMenu, model.StageTargetSearch, model.StageSelection, model.StageConnecting, model.StageAuthPrompt:
		return stage
	case model.StageRemoteShell:
		if pane.HostResolvedName != "" || pane.HostQuery != "" {
			return model.StageRemoteShell
		}
		return model.StageShell
	}

	if observe.ExtractPrompt(text) != "" || observe.IsShellLikeCommand(currentCommand) || strings.TrimSpace(currentCommand) == "" {
		if pane.HostResolvedName != "" || pane.HostQuery != "" {
			return model.StageRemoteShell
		}
		return model.StageShell
	}
	if pane.HostResolvedName != "" || pane.HostQuery != "" {
		return model.StageRemoteShell
	}
	return model.StageUnknown
}

func (s *Service) saveLocked() error {
	if err := s.store.SaveState(s.state); err != nil {
		return err
	}
	return s.store.SaveActions(s.actions)
}

func (s *Service) touchLocked() {
	s.state.LastRequestAt = time.Now().UTC()
}

func (s *Service) statusLocked() model.BrokerStatus {
	workspaces := model.SortedWorkspaces(s.state)
	panes := model.SortedPanes(s.state)
	pendingCount := 0
	runningCount := 0
	for _, action := range s.actions {
		if action.ApprovalState == model.ApprovalPending {
			pendingCount++
		}
	}
	for _, pane := range panes {
		if pane.Mode == model.ModeRunning {
			runningCount++
		}
	}
	return model.BrokerStatus{
		StartedAt:          s.state.StartedAt,
		LastRequestAt:      s.state.LastRequestAt,
		WorkspaceCount:     len(workspaces),
		PaneCount:          len(panes),
		PendingActionCount: pendingCount,
		RunningPaneCount:   runningCount,
		Workspaces:         workspaces,
		Panes:              panes,
	}
}

func (s *Service) shouldAutoExitLocked(now time.Time) bool {
	if now.Sub(s.state.LastRequestAt) < s.idleTimeout {
		return false
	}
	if slices.ContainsFunc(s.actions, func(action model.Action) bool {
		return action.ApprovalState == model.ApprovalPending || action.Status == model.ActionSent
	}) {
		return false
	}
	activeWorkspace := false
	activePane := false
	for _, workspace := range s.state.Workspaces {
		if workspace.Status != model.WorkspaceClosed {
			activeWorkspace = true
			break
		}
	}
	for _, pane := range s.state.Panes {
		if pane.Mode != model.ModeDisconnected {
			activePane = true
			break
		}
	}
	return !activeWorkspace || !activePane
}

func (s *Service) launchCommandForPane(pane model.Pane) string {
	return "/bin/zsh -lc " + execx.ShellQuote(s.tmux.AttachCommand(pane.LocalTmuxSession))
}

func toSnapshot(pane model.Pane) model.PaneSnapshot {
	return model.PaneSnapshot{
		PaneID:           pane.ID,
		Text:             pane.LastSnapshot,
		UpdatedAt:        pane.LastSnapshotAt,
		Mode:             pane.Mode,
		Stage:            pane.Stage,
		Controller:       pane.Controller,
		Prompt:           pane.LastPrompt,
		SnapshotHash:     pane.LastSnapshotHash,
		LocalSession:     pane.LocalTmuxSession,
		LocalTarget:      pane.LocalTmuxTarget,
		RemoteProvider:   pane.RemoteProvider,
		RemoteSession:    pane.RemoteTmuxSession,
		RemoteTmuxStatus: pane.RemoteTmuxStatus,
		RemoteTmuxDetail: pane.RemoteTmuxDetail,
	}
}

func decodeParams(raw json.RawMessage, dest any) error {
	if dest == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return newError(rpc.ReasonInvalidState, err)
	}
	return nil
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
