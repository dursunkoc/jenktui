package sysint

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNotifyThroughTheTerminal(t *testing.T) {
	const title, body = "jenktui", "FAILED api/deploy #57\x1b]0;x\a"
	tests := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, "\x1b]9;jenktui: FAILED api/deploy #57 ]0;x \a"},
		{map[string]string{"TERM_PROGRAM": "ghostty"}, "\x1b]777;notify;jenktui;FAILED api/deploy #57 ]0;x \a"},
		{map[string]string{"TERM_PROGRAM": "WezTerm"}, "\x1b]777;notify;jenktui;FAILED api/deploy #57 ]0;x \a"},
		{map[string]string{"TERM": "xterm-kitty"}, "\x1b]99;i=jenktui:d=0;jenktui\x1b\\\x1b]99;i=jenktui:d=1:p=body;FAILED api/deploy #57 ]0;x \x1b\\"},
	}
	for _, tc := range tests {
		r := &recorder{}
		var out bytes.Buffer
		s := Sys{Run: r.run, GOOS: "darwin", Getenv: env(tc.env), Stdout: &out}
		if err := s.Notify(title, body); err != nil {
			t.Fatal(err)
		}
		if out.String() != tc.want || len(r.calls) != 0 {
			t.Errorf("%v: out %q calls %v", tc.env, out.String(), r.calls)
		}
	}
}

func TestNotifyFallsBackToTheOS(t *testing.T) {
	for _, e := range []map[string]string{
		{"TERM_PROGRAM": "Apple_Terminal"},
		{"TERM_PROGRAM": "iTerm.app", "TMUX": "/tmp/tmux-1/default,1,0"},
	} {
		r := &recorder{}
		var out bytes.Buffer
		s := Sys{Run: r.run, GOOS: "darwin", Getenv: env(e), Stdout: &out}
		if err := s.Notify("t", "b"); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 || len(r.calls) != 1 || r.calls[0].Name != "osascript" {
			t.Errorf("%v: out %q calls %v", e, out.String(), r.calls)
		}
	}
	// a known terminal without Stdout still notifies
	r := &recorder{}
	s := Sys{Run: r.run, GOOS: "darwin", Getenv: env(map[string]string{"TERM_PROGRAM": "iTerm.app"})}
	if err := s.Notify("t", "b"); err != nil || len(r.calls) != 1 {
		t.Fatalf("err %v calls %v", err, r.calls)
	}
}

func TestNotifyLinuxIcon(t *testing.T) {
	dir := t.TempDir()
	r := &recorder{}
	s := newSys("linux", r, nil)
	s.CacheDir = dir
	if err := s.Notify("t", "b"); err != nil {
		t.Fatal(err)
	}
	icon := filepath.Join(dir, "icon.png")
	want := []call{{Name: "notify-send", Args: []string{"-i", icon, "--", "t", "b"}}}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v", r.calls)
	}
	if b, err := os.ReadFile(icon); err != nil || !bytes.Equal(b, iconPNG) {
		t.Fatalf("icon: %v", err)
	}
}
