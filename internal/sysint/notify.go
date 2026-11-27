package sysint

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// iconPNG is jenktui's icon (docs/icon/make_icon.py), shown by notify-send.
//
//go:embed icon.png
var iconPNG []byte

// Notify shows a desktop notification. A terminal that posts notifications
// itself (iTerm2, Ghostty, WezTerm, kitty; not inside tmux) gets an escape
// sequence on Stdout: the notification is the terminal's, and clicking it
// brings the terminal, and so jenktui, to the front. Otherwise it is
// osascript on macOS, notify-send with jenktui's icon on linux, and nothing
// on windows. Neither needs an app of jenktui's own or admin rights.
func (s Sys) Notify(title, body string) error {
	if seq := s.termNotice(title, body); seq != "" && s.Stdout != nil {
		if _, err := io.WriteString(s.Stdout, seq); err == nil {
			return nil
		}
	}
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
		var args []string
		if icon := s.iconFile(); icon != "" {
			args = append(args, "-i", icon)
		}
		return s.Run("notify-send", append(args, "--", title, body)...)
	case "windows":
		return nil
	}
	return fmt.Errorf("sysint: notify is not supported on %q", s.GOOS)
}

// appleScript reads title and body from argv, so user text is never part of
// the script source.
var appleScript = []string{
	"on run argv",
	"display notification (item 2 of argv) with title (item 1 of argv)",
	"end run",
}

// termNotice is the escape sequence that makes the terminal post the
// notification, or "" when the terminal is not known to support one. Inside
// tmux it is always "": tmux drops these unless passthrough is turned on.
func (s Sys) termNotice(title, body string) string {
	if s.env("TMUX") != "" {
		return ""
	}
	title, body = plain(title), plain(body)
	switch {
	case s.env("TERM_PROGRAM") == "iTerm.app":
		return "\x1b]9;" + title + ": " + body + "\a"
	case s.env("TERM_PROGRAM") == "ghostty", s.env("TERM_PROGRAM") == "WezTerm":
		return "\x1b]777;notify;" + strings.ReplaceAll(title, ";", ",") + ";" + body + "\a"
	case s.env("KITTY_WINDOW_ID") != "", s.env("TERM") == "xterm-kitty":
		return "\x1b]99;i=jenktui:d=0;" + title + "\x1b\\" +
			"\x1b]99;i=jenktui:d=1:p=body;" + body + "\x1b\\"
	}
	return ""
}

// plain drops control characters, which could end the escape sequence early.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r >= 0x7f && r < 0xa0 {
			return ' '
		}
		return r
	}, s)
}

// iconFile writes the icon to the cache (once) and returns its path, or ""
// when there is no cache.
func (s Sys) iconFile() string {
	if s.CacheDir == "" {
		return ""
	}
	p := filepath.Join(s.CacheDir, "icon.png")
	if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, iconPNG) {
		return p
	}
	if os.MkdirAll(s.CacheDir, 0o700) != nil || os.WriteFile(p, iconPNG, 0o644) != nil {
		return ""
	}
	return p
}
