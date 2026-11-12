// Package sysint wraps the few operating-system integrations jenktui needs:
// opening a URL in the browser, copying text to the clipboard and notifying
// the user. Every external command goes through an injectable runner so that
// tests never start a real process.
package sysint

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Runner runs an external command. In production it is exec.Command(...).Run.
type Runner func(name string, args ...string) error

// InputRunner runs an external command with stdin set to input.
type InputRunner func(input string, name string, args ...string) error

// Sys performs the integrations. The zero value runs nothing and returns
// errors; use New for the real thing.
type Sys struct {
	Run   Runner
	RunIn InputRunner // commands that read stdin (pbcopy, xclip, clip.exe)
	GOOS  string
	// Stdout receives OSC 52 sequences and the bell; nil disables both.
	Stdout io.Writer
}

// New returns a Sys for the running OS that executes real commands and
// writes terminal escapes to os.Stdout.
func New() Sys {
	return Sys{
		Run: func(name string, args ...string) error {
			return exec.Command(name, args...).Run()
		},
		RunIn: func(input string, name string, args ...string) error {
			c := exec.Command(name, args...)
			c.Stdin = strings.NewReader(input)
			return c.Run()
		},
		GOOS:   runtime.GOOS,
		Stdout: os.Stdout,
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
	switch s.GOOS {
	case "darwin":
		return s.Run("open", rawURL)
	case "linux":
		return s.Run("xdg-open", rawURL)
	case "windows":
		return s.Run("rundll32", "url.dll,FileProtocolHandler", rawURL)
	}
	return fmt.Errorf("sysint: open is not supported on %q", s.GOOS)
}

// osc52 returns the escape sequence that asks the terminal to set the
// clipboard to text.
func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}

// Copy puts text on the clipboard. It writes an OSC 52 sequence to Stdout
// first (works over SSH and tmux), then tries the platform tool. It fails
// only when neither route worked.
func (s Sys) Copy(text string) error {
	wrote := false
	if s.Stdout != nil {
		if _, err := io.WriteString(s.Stdout, osc52(text)); err == nil {
			wrote = true
		}
	}
	err := s.copyFallback(text)
	if err != nil && wrote {
		return nil
	}
	return err
}

func (s Sys) copyFallback(text string) error {
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
