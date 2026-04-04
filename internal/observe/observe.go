package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/Woo-kk/tmux-ghostty/internal/model"
)

var (
	promptRE            = regexp.MustCompile(`(?m)^(?:\([^)]+\)\s*)?(?:\[[^\]]+\][#$%]|[^\s@]+@[^\s:]+[: ][^\n]*[#$]|[^ \t]+[#$%])\s*$`)
	interactiveCommands = map[string]struct{}{
		"vim":     {},
		"vi":      {},
		"less":    {},
		"top":     {},
		"htop":    {},
		"mysql":   {},
		"psql":    {},
		"python":  {},
		"ipython": {},
		"node":    {},
		"irb":     {},
	}
	shellCommands = map[string]struct{}{
		"bash":    {},
		"zsh":     {},
		"sh":      {},
		"fish":    {},
		"tmux":    {},
		"ssh":     {},
		"expect":  {},
		"python3": {},
	}
)

func HashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func ExtractPrompt(text string) string {
	lines := strings.Split(text, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		if promptRE.MatchString(line) {
			return line
		}
		return ""
	}
	return ""
}

func LikelyIdle(text string) bool {
	return ExtractPrompt(text) != ""
}

func IsInteractiveCommand(command string) bool {
	_, ok := interactiveCommands[strings.TrimSpace(strings.ToLower(command))]
	return ok
}

func IsShellLikeCommand(command string) bool {
	_, ok := shellCommands[strings.TrimSpace(strings.ToLower(command))]
	return ok
}

func ModeFromCommand(command string) (model.PaneMode, bool) {
	if IsInteractiveCommand(command) {
		return model.ModeObserveOnly, true
	}
	if IsShellLikeCommand(command) {
		return model.ModeIdle, true
	}
	if strings.TrimSpace(command) == "" {
		return model.ModeIdle, true
	}
	return model.ModeRunning, true
}
