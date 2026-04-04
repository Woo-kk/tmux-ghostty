package main

import (
	"fmt"
	"os"

	"github.com/Woo-kk/tmux-ghostty/internal/app"
)

func main() {
	if err := app.RunBrokerProcess(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
