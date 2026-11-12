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
	s := Sys{Run: r.run, RunIn: r.runIn, GOOS: goos}
	if out != nil {
		s.Stdout = out
	}
	return s
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

func TestCopy(t *testing.T) {
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
			if err := newSys(tc.goos, r, &out).Copy(text); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r.calls, tc.want) {
				t.Fatalf("calls = %#v, want %#v", r.calls, tc.want)
			}
			want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
			if out.String() != want {
				t.Fatalf("osc52 = %q, want %q", out.String(), want)
			}
		})
	}
}

func TestCopyLinuxFallsBackToXclip(t *testing.T) {
	r := &recorder{fail: map[string]bool{"wl-copy": true}}
	if err := newSys("linux", r, nil).Copy("x"); err != nil {
		t.Fatal(err)
	}
	want := []call{
		{Input: "x", Name: "wl-copy"},
		{Input: "x", Name: "xclip", Args: []string{"-selection", "clipboard"}},
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v", r.calls)
	}
}

func TestCopyErrors(t *testing.T) {
	// Tool fails and no OSC 52 was written: error.
	r := &recorder{fail: map[string]bool{"pbcopy": true}}
	if err := newSys("darwin", r, nil).Copy("x"); err == nil {
		t.Error("expected error when nothing copied")
	}
	// Tool fails but OSC 52 went out: success.
	var out bytes.Buffer
	if err := newSys("darwin", r, &out).Copy("x"); err != nil {
		t.Errorf("OSC 52 written, got %v", err)
	}
	// Unsupported OS without a terminal: error; with a terminal: ok.
	if err := newSys("plan9", &recorder{}, nil).Copy("x"); err == nil {
		t.Error("unsupported GOOS without Stdout should fail")
	}
	if err := newSys("plan9", &recorder{}, &out).Copy("x"); err != nil {
		t.Errorf("unsupported GOOS with OSC 52: %v", err)
	}
	if err := (Sys{GOOS: "darwin"}).Copy("x"); err == nil {
		t.Error("zero Sys should fail")
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
