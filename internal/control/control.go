package control

import (
	"fmt"

	"github.com/guyuanshun/tmux-ghostty/internal/model"
)

func Claim(pane model.Pane, actor model.Controller) model.Pane {
	pane.Controller = actor
	if actor == model.ControllerUser && pane.Mode == model.ModeAwaitingApproval {
		pane.Mode = model.ModeIdle
	}
	return pane
}

func Release(pane model.Pane) model.Pane {
	pane.Controller = model.ControllerUser
	if pane.Mode == model.ModeAwaitingApproval {
		pane.Mode = model.ModeIdle
	}
	return pane
}

func Observe(pane model.Pane) model.Pane {
	pane.Mode = model.ModeObserveOnly
	return pane
}

func RequireAgentControl(pane model.Pane) error {
	if pane.Controller != model.ControllerAgent {
		return fmt.Errorf("not_controller")
	}
	return nil
}
