// Package sysint wraps the few operating-system integrations jenktui needs:
// opening a URL in the browser, copying text to the clipboard and notifying
// the user. Every external command goes through an injectable runner so that
// tests never start a real process.
package sysint

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Runner runs an external command. In production it is exec.Command(...).Run.
type Runner func(name string, args ...string) error

// InputRunner runs an external command with stdin set to input.
type InputRunner func(input string, name string, args ...string) error

// Route says how Copy delivered the text.
type Route int

const (
	// RouteTool: a platform clipboard tool exited successfully (verified).
	RouteTool Route = iota + 1
	// RouteTerminal: the text was only handed to the terminal (OSC 52 or
	// tmux); the terminal may still drop it (unverified).
	RouteTerminal
)

// Sys performs the integrations. The zero value runs nothing and returns
// errors; use New for the real thing.
type Sys struct {
	Run   Runner
	RunIn InputRunner // commands that read stdin (pbcopy, xclip, clip.exe, tmux)
	GOOS  string
	// Getenv reads environment variables (DISPLAY, WAYLAND_DISPLAY, TMUX).
	// Nil means an empty environment.
	Getenv func(string) string
	// Stdout receives OSC 52 sequences and the bell; nil disables both.
	Stdout io.Writer
}

// commandTimeout bounds every command started by New's runners.
const commandTimeout = 10 * time.Second

// New returns a Sys for the running OS that executes real commands and
// writes terminal escapes to out (normally os.Stdout).
func New(out io.Writer) Sys {
	return Sys{
		Run: func(name string, args ...string) error {
			ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
			defer cancel()
			return exec.CommandContext(ctx, name, args...).Run()
		},
		RunIn: func(input string, name string, args ...string) error {
			ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
			defer cancel()
			c := exec.CommandContext(ctx, name, args...)
			c.Stdin = strings.NewReader(input)
			// Stdout and Stderr stay nil (/dev/null) on purpose: wl-copy forks
			// a background process that keeps inherited pipes open, which
			// would make Wait hang forever.
			return c.Run()
		},
		GOOS:   runtime.GOOS,
		Getenv: os.Getenv,
		Stdout: out,
	}
}

var errNoRunner = errors.New("sysint: no command runner configured")

// Open opens an http or https URL in the default browser.
func (s Sys) Open(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("sysint: refusing to open %q: only http and https URLs are allowed", rawURL)
	}
	if s.Run == nil {
		return errNoRunner
	}
	target := u.String()
	switch s.GOOS {
	case "darwin":
		return s.Run("open", target)
	case "linux":
		// Without a graphical session xdg-open may start a text browser on
		// our tty and fight the TUI for it.
		if s.env("DISPLAY") == "" && s.env("WAYLAND_DISPLAY") == "" {
			return errors.New("sysint: no graphical session (DISPLAY and WAYLAND_DISPLAY are empty)")
		}
		return s.Run("xdg-open", target)
	case "windows":
		return s.Run("rundll32", "url.dll,FileProtocolHandler", target)
	}
	return fmt.Errorf("sysint: open is not supported on %q", s.GOOS)
}

// osc52 returns the escape sequence that asks the terminal to set the
// clipboard to text.
func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}

func (s Sys) env(k string) string {
	if s.Getenv == nil {
		return ""
	}
	return s.Getenv(k)
}

// Copy puts text on the clipboard. It tries the platform tool first; success
// is RouteTool. If the tool fails it falls back to the terminal, which cannot
// confirm anything, so success there is RouteTerminal and callers should say
// "sent to terminal clipboard" rather than "copied".
//
// Inside tmux ($TMUX set) the fallback is "tmux load-buffer -w -": tmux then
// sets its buffer and forwards to the outer terminal according to its own
// set-clipboard setting. This is preferred over DCS passthrough, which is off
// by default since tmux 3.3 (allow-passthrough), and a raw OSC 52 is ignored
// by tmux's default "set-clipboard external". Outside tmux the fallback is an
// OSC 52 sequence written to Stdout (works over SSH when the terminal allows
// it).
func (s Sys) Copy(text string) (Route, error) {
	toolErr := s.copyTool(text)
	if toolErr == nil {
		return RouteTool, nil
	}
	if s.env("TMUX") != "" && s.RunIn != nil {
		if err := s.RunIn(text, "tmux", "load-buffer", "-w", "-"); err == nil {
			return RouteTerminal, nil
		}
	} else if s.Stdout != nil {
		if _, err := io.WriteString(s.Stdout, osc52(text)); err == nil {
			return RouteTerminal, nil
		}
	}
	return 0, toolErr
}

func (s Sys) copyTool(text string) error {
	if s.RunIn == nil {
		return errNoRunner
	}
	switch s.GOOS {
	case "darwin":
		return s.RunIn(text, "pbcopy")
	case "linux":
		if err := s.RunIn(text, "wl-copy"); err == nil {
			return nil
		}
		return s.RunIn(text, "xclip", "-selection", "clipboard")
	case "windows":
		return s.RunIn(text, "clip.exe")
	}
	return fmt.Errorf("sysint: copy is not supported on %q", s.GOOS)
}

// appleScript reads title and body from argv, so user text is never part of
// the script source.
var appleScript = []string{
	"on run argv",
	"display notification (item 2 of argv) with title (item 1 of argv)",
	"end run",
}

// Notify shows a desktop notification. It is a no-op on windows.
func (s Sys) Notify(title, body string) error {
	switch s.GOOS {
	case "darwin":
		if s.Run == nil {
			return errNoRunner
		}
		args := make([]string, 0, 9)
		for _, line := range appleScript {
			args = append(args, "-e", line)
		}
		args = append(args, "--", title, body)
		return s.Run("osascript", args...)
	case "linux":
		if s.Run == nil {
			return errNoRunner
		}
		return s.Run("notify-send", "--", title, body)
	case "windows":
		return nil
	}
	return fmt.Errorf("sysint: notify is not supported on %q", s.GOOS)
}

// Bell rings the terminal bell.
func (s Sys) Bell() {
	if s.Stdout != nil {
		_, _ = io.WriteString(s.Stdout, "\a")
	}
}
