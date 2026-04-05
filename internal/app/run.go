package app

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Woo-kk/tmux-ghostty/internal/broker"
	"github.com/Woo-kk/tmux-ghostty/internal/execx"
	"github.com/Woo-kk/tmux-ghostty/internal/ghostty"
	"github.com/Woo-kk/tmux-ghostty/internal/logx"
	"github.com/Woo-kk/tmux-ghostty/internal/remote"
	"github.com/Woo-kk/tmux-ghostty/internal/rpc"
	"github.com/Woo-kk/tmux-ghostty/internal/tmux"
)

func RunBrokerProcess() error {
	paths, err := DefaultPaths()
	if err != nil {
		return err
	}
	if err := paths.EnsureBaseDir(); err != nil {
		return err
	}

	logger, err := logx.New(paths.LogPath)
	if err != nil {
		return err
	}
	defer logger.Close()

	runner := execx.NewRunner(logger)
	tmuxClient := tmux.New(runner)
	ghosttyClient := ghostty.New(runner)
	remoteClient := remote.New(tmuxClient)

	service, err := broker.NewService(paths.StatePath, paths.ActionsPath, IdleTimeout(), logger, ghosttyClient, tmuxClient, remoteClient)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	service.SetShutdownFunc(cancel)
	service.Start(ctx)

	if err := WritePID(paths, os.Getpid()); err != nil {
		return err
	}
	defer RemoveRuntimeFiles(paths)

	server := rpc.Server{
		SocketPath: paths.SocketPath,
		Log:        logger,
		Handler:    service.HandleRPC,
	}
	return server.Listen(ctx)
}
