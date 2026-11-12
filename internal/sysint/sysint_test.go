package sysint

import (
	"bytes"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type call struct {
	Input string
	Name  string
	Args  []string
}

type recorder struct {
	calls []call
	fail  map[string]bool // command names that fail
}

func (r *recorder) run(name string, args ...string) error {
	r.calls = append(r.calls, call{Name: name, Args: args})
	if r.fail[name] {
		return errors.New("boom")
	}
	return nil
}

func (r *recorder) runIn(in, name string, args ...string) error {
	r.calls = append(r.calls, call{Input: in, Name: name, Args: args})
	if r.fail[name] {
		return errors.New("boom")
	}
	return nil
}

func newSys(goos string, r *recorder, out *bytes.Buffer) Sys {
	s := Sys{Run: r.run, RunIn: r.runIn, GOOS: goos, Getenv: env(map[string]string{"DISPLAY": ":0"})}
	if out != nil {
		s.Stdout = out
	}
	return s
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestOpen(t *testing.T) {
	const u = "https://jenkins.example/job/a/1/"
	tests := []struct {
		goos string
		want call
	}{
		{"darwin", call{Name: "open", Args: []string{u}}},
		{"linux", call{Name: "xdg-open", Args: []string{u}}},
		{"windows", call{Name: "rundll32", Args: []string{"url.dll,FileProtocolHandler", u}}},
	}
	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			r := &recorder{}
			if err := newSys(tc.goos, r, nil).Open(u); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r.calls, []call{tc.want}) {
				t.Fatalf("calls = %#v, want %#v", r.calls, tc.want)
			}
		})
	}
}

func TestOpenRefusesOtherSchemes(t *testing.T) {
	for _, u := range []string{
		"file:///etc/passwd", "javascript:alert(1)", "ftp://x/y", "", "//host/x",
		"/relative", "https://", "-a Calculator", "data:text/html,hi",
	} {
		r := &recorder{}
		if err := newSys("darwin", r, nil).Open(u); err == nil {
			t.Errorf("Open(%q) succeeded", u)
		}
		if len(r.calls) != 0 {
			t.Errorf("Open(%q) ran %v", u, r.calls)
		}
	}
	if err := newSys("darwin", &recorder{}, nil).Open("http://h/x"); err != nil {
		t.Errorf("http refused: %v", err)
	}
}

func TestOpenUnsupportedAndNoRunner(t *testing.T) {
	if err := newSys("plan9", &recorder{}, nil).Open("https://h/"); err == nil {
		t.Error("unsupported GOOS should fail")
	}
	if err := (Sys{GOOS: "darwin"}).Open("https://h/"); err == nil {
		t.Error("nil runner should fail")
	}
}

func TestCopyTool(t *testing.T) {
	const text = "https://jenkins.example/job/a b/1/ é"
	tests := []struct {
		goos string
		want []call
	}{
		{"darwin", []call{{Input: text, Name: "pbcopy"}}},
		{"linux", []call{{Input: text, Name: "wl-copy"}}},
		{"windows", []call{{Input: text, Name: "clip.exe"}}},
	}
	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			r := &recorder{}
			var out bytes.Buffer
			route, err := newSys(tc.goos, r, &out).Copy(text)
			if err != nil || route != RouteTool {
				t.Fatalf("route=%v err=%v", route, err)
			}
			if !reflect.DeepEqual(r.calls, tc.want) {
				t.Fatalf("calls = %#v, want %#v", r.calls, tc.want)
			}
			if out.Len() != 0 {
				t.Fatalf("OSC 52 written despite tool success: %q", out.String())
			}
		})
	}
}

func TestCopyLinuxFallsBackToXclip(t *testing.T) {
	r := &recorder{fail: map[string]bool{"wl-copy": true}}
	route, err := newSys("linux", r, nil).Copy("x")
	if err != nil || route != RouteTool {
		t.Fatal(route, err)
	}
	want := []call{
		{Input: "x", Name: "wl-copy"},
		{Input: "x", Name: "xclip", Args: []string{"-selection", "clipboard"}},
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v", r.calls)
	}
}

func TestCopyTerminalFallback(t *testing.T) {
	const text = "héllo"
	r := &recorder{fail: map[string]bool{"pbcopy": true}}
	var out bytes.Buffer
	route, err := newSys("darwin", r, &out).Copy(text)
	if err != nil || route != RouteTerminal {
		t.Fatalf("route=%v err=%v", route, err)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
	if out.String() != want {
		t.Fatalf("osc52 = %q, want %q", out.String(), want)
	}
}

func TestCopyInTmux(t *testing.T) {
	r := &recorder{fail: map[string]bool{"pbcopy": true}}
	var out bytes.Buffer
	s := newSys("darwin", r, &out)
	s.Getenv = env(map[string]string{"TMUX": "/tmp/tmux-1/default,1,0"})
	route, err := s.Copy("x")
	if err != nil || route != RouteTerminal {
		t.Fatalf("route=%v err=%v", route, err)
	}
	want := []call{
		{Input: "x", Name: "pbcopy"},
		{Input: "x", Name: "tmux", Args: []string{"load-buffer", "-w", "-"}},
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v", r.calls)
	}
	if out.Len() != 0 {
		t.Fatalf("raw OSC 52 written inside tmux: %q", out.String())
	}
}

func TestCopyErrors(t *testing.T) {
	r := &recorder{fail: map[string]bool{"pbcopy": true}}
	if _, err := newSys("darwin", r, nil).Copy("x"); err == nil {
		t.Error("expected error when nothing copied")
	}
	r = &recorder{fail: map[string]bool{"pbcopy": true, "tmux": true}}
	s := newSys("darwin", r, nil)
	s.Getenv = env(map[string]string{"TMUX": "1"})
	if _, err := s.Copy("x"); err == nil {
		t.Error("expected error when tmux also fails")
	}
	var out bytes.Buffer
	if route, err := newSys("plan9", &recorder{}, &out).Copy("x"); err != nil || route != RouteTerminal {
		t.Errorf("unsupported GOOS with terminal: %v %v", route, err)
	}
	if _, err := (Sys{GOOS: "darwin"}).Copy("x"); err == nil {
		t.Error("zero Sys should fail")
	}
}

func TestOpenLinuxNeedsGraphicalSession(t *testing.T) {
	r := &recorder{}
	s := newSys("linux", r, nil)
	s.Getenv = env(nil)
	if err := s.Open("https://h/"); err == nil || !strings.Contains(err.Error(), "no graphical session") {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("ran %v", r.calls)
	}
	s.Getenv = env(map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
	if err := s.Open("https://h/"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenPassesEncodedURL(t *testing.T) {
	r := &recorder{}
	if err := newSys("darwin", r, nil).Open(`https://h/job/a b"c/1/`); err != nil {
		t.Fatal(err)
	}
	if got := r.calls[0].Args[0]; got != "https://h/job/a%20b%22c/1/" {
		t.Fatalf("arg = %q", got)
	}
}

func TestNotify(t *testing.T) {
	const title, body = "jenktui", `FAILED "api/deploy" #57 \ ' ; do shell script "x"`
	t.Run("darwin", func(t *testing.T) {
		r := &recorder{}
		if err := newSys("darwin", r, nil).Notify(title, body); err != nil {
			t.Fatal(err)
		}
		if len(r.calls) != 1 || r.calls[0].Name != "osascript" {
			t.Fatalf("calls = %#v", r.calls)
		}
		args := r.calls[0].Args
		// user text appears only as trailing argv, never inside a script line
		if n := len(args); args[n-3] != "--" || args[n-2] != title || args[n-1] != body {
			t.Fatalf("args = %q", args)
		}
		for _, a := range args[:len(args)-2] {
			if strings.Contains(a, "FAILED") || strings.Contains(a, "jenktui") {
				t.Fatalf("user text in script: %q", a)
			}
		}
		if args[0] != "-e" || args[1] != "on run argv" {
			t.Fatalf("args = %q", args)
		}
	})
	t.Run("linux", func(t *testing.T) {
		r := &recorder{}
		if err := newSys("linux", r, nil).Notify(title, body); err != nil {
			t.Fatal(err)
		}
		want := []call{{Name: "notify-send", Args: []string{"--", title, body}}}
		if !reflect.DeepEqual(r.calls, want) {
			t.Fatalf("calls = %#v", r.calls)
		}
	})
	t.Run("windows is a no-op", func(t *testing.T) {
		r := &recorder{}
		if err := newSys("windows", r, nil).Notify(title, body); err != nil || len(r.calls) != 0 {
			t.Fatalf("err=%v calls=%v", err, r.calls)
		}
	})
	t.Run("runner error", func(t *testing.T) {
		r := &recorder{fail: map[string]bool{"notify-send": true}}
		if err := newSys("linux", r, nil).Notify(title, body); err == nil {
			t.Error("expected error")
		}
	})
}

func TestBell(t *testing.T) {
	var out bytes.Buffer
	newSys("linux", &recorder{}, &out).Bell()
	if out.String() != "\a" {
		t.Fatalf("bell = %q", out.String())
	}
	Sys{}.Bell() // nil Stdout must not panic
}
