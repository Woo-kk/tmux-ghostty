package ghostty

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/execx"
)

const (
	appName       = "Ghostty"
	appBundleID   = "com.mitchellh.ghostty"
	scriptTimeout = 8 * time.Second
	recordSep     = "\x1e"
	fieldSep      = "\x1f"
)

type WindowRef struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SelectedTabID string `json:"selected_tab_id"`
}

type TabRef struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Index             int    `json:"index"`
	Selected          bool   `json:"selected"`
	FocusedTerminalID string `json:"focused_terminal_id"`
}

type TerminalRef struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	WorkingDirectory string `json:"working_directory"`
}

type Client struct {
	runner *execx.Runner
}

func New(runner *execx.Runner) *Client {
	return &Client{runner: runner}
}

func (c *Client) Available() error {
	_, err := c.runScript(`tell application id "` + appBundleID + `" to return version`)
	return err
}

func (c *Client) EnsureRunning() error {
	if err := c.Available(); err == nil {
		return nil
	}
	if _, err := c.runner.Run(context.Background(), scriptTimeout, "open", "-a", appName); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := c.Available(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("ghostty did not become available")
	}
	return lastErr
}

func (c *Client) NewWindow(initialCommand string) (WindowRef, TerminalRef, error) {
	output, err := c.runScript(withHelpers(`
tell application id "` + appBundleID + `"
  set cfg to new surface configuration
  set command of cfg to "` + appleScriptQuote(initialCommand) + `"
  set wait after command of cfg to true
  set win to new window with configuration cfg
  set tabRef to selected tab of win
  set termRef to focused terminal of tabRef
  set cwd to ""
  try
    set cwd to working directory of termRef
  end try
  return my joinFields({id of win, name of win, id of tabRef, id of termRef, name of termRef, cwd})
end tell
`))
	if err != nil {
		return WindowRef{}, TerminalRef{}, err
	}
	fields := strings.Split(output, fieldSep)
	if len(fields) != 6 {
		return WindowRef{}, TerminalRef{}, fmt.Errorf("unexpected ghostty response for new window: %q", output)
	}
	return WindowRef{
			ID:            fields[0],
			Name:          fields[1],
			SelectedTabID: fields[2],
		},
		TerminalRef{
			ID:               fields[3],
			Name:             fields[4],
			WorkingDirectory: fields[5],
		},
		nil
}

func (c *Client) NewTab(windowID string, initialCommand string) (TabRef, TerminalRef, error) {
	output, err := c.runScript(withHelpers(`
tell application id "` + appBundleID + `"
  set win to first window whose id is "` + appleScriptQuote(windowID) + `"
  set cfg to new surface configuration
  set command of cfg to "` + appleScriptQuote(initialCommand) + `"
  set wait after command of cfg to true
  set tabRef to new tab in win with configuration cfg
  set termRef to focused terminal of tabRef
  set cwd to ""
  try
    set cwd to working directory of termRef
  end try
  return my joinFields({id of tabRef, name of tabRef, (index of tabRef) as text, my boolText(selected of tabRef), id of termRef, name of termRef, cwd})
end tell
`))
	if err != nil {
		return TabRef{}, TerminalRef{}, err
	}
	fields := strings.Split(output, fieldSep)
	if len(fields) != 7 {
		return TabRef{}, TerminalRef{}, fmt.Errorf("unexpected ghostty response for new tab: %q", output)
	}
	return TabRef{
			ID:                fields[0],
			Name:              fields[1],
			Index:             parseInt(fields[2]),
			Selected:          fields[3] == "true",
			FocusedTerminalID: fields[4],
		},
		TerminalRef{
			ID:               fields[4],
			Name:             fields[5],
			WorkingDirectory: fields[6],
		},
		nil
}

func (c *Client) SplitTerminal(terminalID string, direction string, initialCommand string) (TerminalRef, error) {
	dir := strings.ToLower(strings.TrimSpace(direction))
	switch dir {
	case "right", "left", "down", "up":
	default:
		return TerminalRef{}, fmt.Errorf("unsupported split direction: %q", direction)
	}
	output, err := c.runScript(withHelpers(`
tell application id "` + appBundleID + `"
  set termRef to first terminal whose id is "` + appleScriptQuote(terminalID) + `"
  set cfg to new surface configuration
  set command of cfg to "` + appleScriptQuote(initialCommand) + `"
  set wait after command of cfg to true
  set newTerm to split termRef direction ` + dir + ` with configuration cfg
  set cwd to ""
  try
    set cwd to working directory of newTerm
  end try
  return my joinFields({id of newTerm, name of newTerm, cwd})
end tell
`))
	if err != nil {
		return TerminalRef{}, err
	}
	fields := strings.Split(output, fieldSep)
	if len(fields) != 3 {
		return TerminalRef{}, fmt.Errorf("unexpected ghostty response for split: %q", output)
	}
	return TerminalRef{
		ID:               fields[0],
		Name:             fields[1],
		WorkingDirectory: fields[2],
	}, nil
}

func (c *Client) FocusTerminal(terminalID string) error {
	_, err := c.runScript(`
tell application id "` + appBundleID + `"
  set termRef to first terminal whose id is "` + appleScriptQuote(terminalID) + `"
  focus termRef
end tell
`)
	return err
}

func (c *Client) InputText(terminalID string, text string) error {
	_, err := c.runScript(`
tell application id "` + appBundleID + `"
  set termRef to first terminal whose id is "` + appleScriptQuote(terminalID) + `"
  input text "` + appleScriptQuote(text) + `" to termRef
end tell
`)
	return err
}

func (c *Client) SendKey(terminalID string, key string, modifiers []string) error {
	modifierValue := strings.Join(modifiers, ",")
	_, err := c.runScript(`
tell application id "` + appBundleID + `"
  set termRef to first terminal whose id is "` + appleScriptQuote(terminalID) + `"
  send key "` + appleScriptQuote(key) + `" modifiers "` + appleScriptQuote(modifierValue) + `" to termRef
end tell
`)
	return err
}

func (c *Client) ListWindows() ([]WindowRef, error) {
	output, err := c.runScript(withHelpers(`
tell application id "` + appBundleID + `"
  set rows to {}
  repeat with win in windows
    set end of rows to my joinFields({id of win, name of win, id of selected tab of win})
  end repeat
  return my joinRows(rows)
end tell
`))
	if err != nil {
		return nil, err
	}
	if output == "" {
		return []WindowRef{}, nil
	}
	records := strings.Split(output, recordSep)
	out := make([]WindowRef, 0, len(records))
	for _, record := range records {
		fields := strings.Split(record, fieldSep)
		if len(fields) != 3 {
			continue
		}
		out = append(out, WindowRef{
			ID:            fields[0],
			Name:          fields[1],
			SelectedTabID: fields[2],
		})
	}
	return out, nil
}

func (c *Client) ListTabs(windowID string) ([]TabRef, error) {
	output, err := c.runScript(withHelpers(`
tell application id "` + appBundleID + `"
  set win to first window whose id is "` + appleScriptQuote(windowID) + `"
  set rows to {}
  repeat with tabRef in tabs of win
    set focusedID to ""
    try
      set focusedID to id of focused terminal of tabRef
    end try
    set end of rows to my joinFields({id of tabRef, name of tabRef, (index of tabRef) as text, my boolText(selected of tabRef), focusedID})
  end repeat
  return my joinRows(rows)
end tell
`))
	if err != nil {
		return nil, err
	}
	if output == "" {
		return []TabRef{}, nil
	}
	records := strings.Split(output, recordSep)
	out := make([]TabRef, 0, len(records))
	for _, record := range records {
		fields := strings.Split(record, fieldSep)
		if len(fields) != 5 {
			continue
		}
		out = append(out, TabRef{
			ID:                fields[0],
			Name:              fields[1],
			Index:             parseInt(fields[2]),
			Selected:          fields[3] == "true",
			FocusedTerminalID: fields[4],
		})
	}
	return out, nil
}

func (c *Client) ListTerminals(tabID string) ([]TerminalRef, error) {
	output, err := c.runScript(withHelpers(`
tell application id "` + appBundleID + `"
  set targetTab to first tab whose id is "` + appleScriptQuote(tabID) + `"
  set rows to {}
  repeat with termRef in terminals of targetTab
    set cwd to ""
    try
      set cwd to working directory of termRef
    end try
    set end of rows to my joinFields({id of termRef, name of termRef, cwd})
  end repeat
  return my joinRows(rows)
end tell
`))
	if err != nil {
		return nil, err
	}
	if output == "" {
		return []TerminalRef{}, nil
	}
	records := strings.Split(output, recordSep)
	out := make([]TerminalRef, 0, len(records))
	for _, record := range records {
		fields := strings.Split(record, fieldSep)
		if len(fields) != 3 {
			continue
		}
		out = append(out, TerminalRef{
			ID:               fields[0],
			Name:             fields[1],
			WorkingDirectory: fields[2],
		})
	}
	return out, nil
}

func (c *Client) runScript(script string) (string, error) {
	tmpFile, err := os.CreateTemp("", "tmux-ghostty-*.applescript")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString(script); err != nil {
		tmpFile.Close()
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		return "", err
	}
	result, err := c.runner.Run(context.Background(), scriptTimeout, "osascript", tmpFile.Name())
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func withHelpers(body string) string {
	return `
on boolText(value)
  if value then
    return "true"
  end if
  return "false"
end boolText

on joinFields(fieldList)
  set oldTIDs to AppleScript's text item delimiters
  set AppleScript's text item delimiters to "` + fieldSep + `"
  set joinedValue to fieldList as text
  set AppleScript's text item delimiters to oldTIDs
  return joinedValue
end joinFields

on joinRows(rowList)
  set oldTIDs to AppleScript's text item delimiters
  set AppleScript's text item delimiters to "` + recordSep + `"
  set joinedValue to rowList as text
  set AppleScript's text item delimiters to oldTIDs
  return joinedValue
end joinRows

` + body
}

func appleScriptQuote(value string) string {
	replacer := strings.NewReplacer(
		`\\`, `\\\\`,
		`"`, `\"`,
		"\r", `\r`,
		"\n", `\n`,
	)
	return replacer.Replace(value)
}

func parseInt(value string) int {
	var out int
	fmt.Sscanf(value, "%d", &out)
	return out
}
