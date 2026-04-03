package broker

import (
	"errors"
	"fmt"

	"github.com/guyuanshun/tmux-ghostty/internal/rpc"
)

type BrokerError struct {
	Reason string
	Err    error
}

func (e *BrokerError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Reason
	}
	return fmt.Sprintf("%s: %v", e.Reason, e.Err)
}

func (e *BrokerError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newError(reason string, err error) error {
	return &BrokerError{Reason: reason, Err: err}
}

func toRPCError(err error) *rpc.RPCError {
	if err == nil {
		return nil
	}
	var brokerErr *BrokerError
	if errors.As(err, &brokerErr) {
		code := rpc.CodeInvalidState
		switch brokerErr.Reason {
		case rpc.ReasonBrokerUnavailable:
			code = rpc.CodeBrokerUnavailable
		case rpc.ReasonGhosttyUnavailable:
			code = rpc.CodeGhosttyUnavailable
		case rpc.ReasonTmuxUnavailable:
			code = rpc.CodeTmuxUnavailable
		case rpc.ReasonPaneNotFound:
			code = rpc.CodePaneNotFound
		case rpc.ReasonNotController:
			code = rpc.CodeNotController
		case rpc.ReasonApprovalRequired:
			code = rpc.CodeApprovalRequired
		case rpc.ReasonInvalidState:
			code = rpc.CodeInvalidState
		case rpc.ReasonJumpAttachFailed:
			code = rpc.CodeJumpAttachFailed
		}
		return rpc.NewError(code, brokerErr.Reason, brokerErr.Err.Error())
	}
	return rpc.NewError(rpc.CodeInvalidState, rpc.ReasonInvalidState, err.Error())
}
