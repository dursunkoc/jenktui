package main

// End-to-end harness: drives the real router and the real wiring from
// newDeps (Menu -> Catalog / Collections -> Trigger -> Run Monitor) with tea
// messages, no terminal, against the in-process fake Jenkins.
//
// Commands returned by screens are executed and their messages fed back into
// the router. Timer commands (tea.Tick, tea.Every, cursor blink) are dropped
// unexecuted, so spinners, the splash animation and the monitor's 3s/30s poll
// schedule never make a test slow; the monitor is refreshed with `R` instead.

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/entry"
)

const (
	termW = 120
	termH = 40
	// cmdTimeout is a safety net for an unknown blocking command; known
	// timers are dropped without running.
	cmdTimeout = 5 * time.Second
	maxSteps   = 20000
)

// harness drives one app instance.
type harness struct {
	t      *testing.T
	fake   *jenkinstest.Server
	dir    string // config dir handed to newDeps
	sess   *core.Session
	router *core.Router
	quit   bool // a tea.Quit / core.Quit was seen
	timers int  // timer commands dropped (pending ticks)
}

type appCfg struct {
	noConfig    bool
	configExtra string // appended to the generated config.toml
	configURL   string // overrides the fake's URL
	collections string // initial collections.toml
	start       string // Collection named on the command line
}

type appOpt func(*appCfg)

func withoutConfig() appOpt           { return func(c *appCfg) { c.noConfig = true } }
func withConfigExtra(s string) appOpt { return func(c *appCfg) { c.configExtra += s + "\n" } }
func withConfigURL(u string) appOpt   { return func(c *appCfg) { c.configURL = u } }
func withStart(name string) appOpt    { return func(c *appCfg) { c.start = name } }
func withCollections(toml string) appOpt {
	return func(c *appCfg) { c.collections = toml }
}

// newApp builds the app exactly as main does: newDeps(dir) plus a Splash root,
// with a temp config dir that already holds a config.toml pointing at fake
// (unless withoutConfig).
func newApp(t *testing.T, fake *jenkinstest.Server, opts ...appOpt) *harness {
	t.Helper()
	var c appCfg
	for _, o := range opts {
		o(&c)
	}
	dir := t.TempDir()
	t.Setenv("JENKTUI_CONFIG_DIR", dir)
	if !c.noConfig {
		u := fake.URL
		if c.configURL != "" {
			u = c.configURL
		}
		writeFile(t, filepath.Join(dir, "config.toml"), "jenkins_url = "+quote(u)+"\n"+c.configExtra)
	}
	if c.collections != "" {
		writeFile(t, filepath.Join(dir, "collections.toml"), c.collections)
	}
	deps := newDeps(dir)
	deps.MinSplash = time.Millisecond
	sess := &core.Session{StartCollection: c.start}
	h := &harness{t: t, fake: fake, dir: dir, sess: sess}
	h.router = core.NewRouter(sess, entry.NewSplash(sess, deps))
	return h
}

func quote(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- command execution ----

// isTimer reports whether c is a timer command that would block.
func isTimer(c tea.Cmd) bool {
	f := runtime.FuncForPC(reflect.ValueOf(c).Pointer())
	if f == nil {
		return false
	}
	n := f.Name()
	return strings.Contains(n, "bubbletea.Tick") || strings.Contains(n, "bubbletea.Every") || strings.Contains(n, "BlinkCmd")
}

var cmdSliceType = reflect.TypeOf([]tea.Cmd(nil))

// exec runs one command; ok is false for a dropped timer or a timeout.
func (h *harness) exec(c tea.Cmd) (msg tea.Msg, ok bool) {
	if isTimer(c) {
		h.timers++
		return nil, false
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- c() }()
	select {
	case m := <-ch:
		return m, true
	case <-time.After(cmdTimeout):
		h.t.Logf("e2e: dropped a command that blocked for %s", cmdTimeout)
		return nil, false
	}
}

// run executes cmd and, transitively, every command its messages produce.
func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > maxSteps {
			h.t.Fatalf("e2e: command loop did not settle after %d steps\n%s", maxSteps, h.screen())
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg, ok := h.exec(c)
		if !ok || msg == nil {
			continue
		}
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			h.quit = true
			continue
		}
		// tea.Batch and tea.Sequence produce slices of commands.
		if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().ConvertibleTo(cmdSliceType) {
			cmds := v.Convert(cmdSliceType).Interface().([]tea.Cmd)
			if strings.Contains(v.Type().Name(), "sequence") {
				queue = append(append([]tea.Cmd(nil), cmds...), queue...)
			} else {
				queue = append(queue, cmds...)
			}
			continue
		}
		_, next := h.router.Update(msg)
		queue = append(queue, next)
	}
}

// send delivers msg to the router and settles all resulting commands.
func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	if _, isQuit := msg.(tea.QuitMsg); isQuit {
		h.quit = true
		return
	}
	_, cmd := h.router.Update(msg)
	h.run(cmd)
}

// ---- input ----

var namedKeys = map[string]tea.KeyMsg{
	"enter":      {Type: tea.KeyEnter},
	"esc":        {Type: tea.KeyEsc},
	"up":         {Type: tea.KeyUp},
	"down":       {Type: tea.KeyDown},
	"left":       {Type: tea.KeyLeft},
	"right":      {Type: tea.KeyRight},
	"tab":        {Type: tea.KeyTab},
	"shift+tab":  {Type: tea.KeyShiftTab},
	"shift+up":   {Type: tea.KeyShiftUp},
	"shift+down": {Type: tea.KeyShiftDown},
	"space":      {Type: tea.KeySpace, Runes: []rune{' '}},
	"backspace":  {Type: tea.KeyBackspace},
	"ctrl+u":     {Type: tea.KeyCtrlU},
	"ctrl+c":     {Type: tea.KeyCtrlC},
	"ctrl+p":     {Type: tea.KeyCtrlP},
}

// keyMsg builds the message for a named key ("enter", "shift+down", ...) or a
// single character ("y", "R", "/").
func keyMsg(name string) tea.KeyMsg {
	if k, ok := namedKeys[name]; ok {
		return k
	}
	r := []rune(name)
	if len(r) != 1 {
		panic("unknown key name: " + name)
	}
	if r[0] == ' ' {
		return namedKeys["space"]
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: r}
}

// key sends named keys or single characters one by one.
func (h *harness) key(names ...string) {
	h.t.Helper()
	for _, n := range names {
		h.send(keyMsg(n))
	}
}

// typeText types s one character at a time (a space is the space key).
func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		h.send(keyMsg(string(r)))
	}
}

// clearField empties the focused text input (ctrl+u deletes before the cursor).
func (h *harness) clearField() { h.t.Helper(); h.key("ctrl+u") }

// replaceField clears the focused input and types s.
func (h *harness) replaceField(s string) { h.t.Helper(); h.clearField(); h.typeText(s) }

// ---- output ----

// screenText is the router View with ANSI sequences stripped and trailing
// blanks trimmed, title bar and help bar included.
func (h *harness) screenText() string {
	lines := strings.Split(ansi.Strip(h.router.View()), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func (h *harness) screen() string { return h.screenText() }

// title is the active screen's title.
func (h *harness) title() string { return h.router.Top().Title() }

func contains(text string, subs ...string) bool {
	for _, s := range subs {
		if !has(text, s) {
			return false
		}
	}
	return true
}

// has reports whether text contains sub. A job path such as
// "team/api/build" also matches its form in a list's name column, "team ›
// api › build", so the same expectation works on lists and on screens that
// print the path (Run Monitor, trigger). The list form itself is checked by
// its own tests.
func has(text, sub string) bool {
	if strings.Contains(text, sub) {
		return true
	}
	return strings.Contains(sub, "/") && strings.Contains(text, strings.ReplaceAll(sub, "/", " › "))
}

// expect fails unless every substring is on screen.
func (h *harness) expect(subs ...string) {
	h.t.Helper()
	text := h.screenText()
	for _, s := range subs {
		if !has(text, s) {
			h.t.Fatalf("screen does not contain %q\n--- screen (%s) ---\n%s", s, h.title(), text)
		}
	}
}

// expectNot fails if any substring is on screen.
// branchRow matches a list row that shows name and branch in their own
// columns: "[ ] proj      main     ● SUCCESS".
func branchRow(name, branch string) *regexp.Regexp {
	return regexp.MustCompile(`\[.\] ` + regexp.QuoteMeta(name) + `\s{2,}` + regexp.QuoteMeta(branch) + `(\s|$)`)
}

// expectBranch takes name, branch pairs; each must be a list row.
func (h *harness) expectBranch(pairs ...string) {
	h.t.Helper()
	text := h.screenText()
	for i := 0; i+1 < len(pairs); i += 2 {
		if !branchRow(pairs[i], pairs[i+1]).MatchString(text) {
			h.t.Fatalf("no row with %q and branch %q\n--- screen (%s) ---\n%s", pairs[i], pairs[i+1], h.title(), text)
		}
	}
}

// expectNoBranch takes name, branch pairs; none may be a list row.
func (h *harness) expectNoBranch(pairs ...string) {
	h.t.Helper()
	text := h.screenText()
	for i := 0; i+1 < len(pairs); i += 2 {
		if branchRow(pairs[i], pairs[i+1]).MatchString(text) {
			h.t.Fatalf("unexpected row with %q and branch %q\n--- screen (%s) ---\n%s", pairs[i], pairs[i+1], h.title(), text)
		}
	}
}

func (h *harness) expectNot(subs ...string) {
	h.t.Helper()
	text := h.screenText()
	for _, s := range subs {
		if has(text, s) {
			h.t.Fatalf("screen unexpectedly contains %q\n--- screen (%s) ---\n%s", s, h.title(), text)
		}
	}
}

// expectTitle fails unless the active screen has exactly this title.
func (h *harness) expectTitle(want string) {
	h.t.Helper()
	if got := h.title(); got != want {
		h.t.Fatalf("title = %q, want %q\n%s", got, want, h.screenText())
	}
}

// row returns the first screen line containing sub, failing if none.
func (h *harness) row(sub string) string {
	h.t.Helper()
	for _, l := range strings.Split(h.screenText(), "\n") {
		if has(l, sub) {
			return l
		}
	}
	h.t.Fatalf("no screen line contains %q\n--- screen (%s) ---\n%s", sub, h.title(), h.screenText())
	return ""
}

// expectRow fails unless one line holds sub and every want.
func (h *harness) expectRow(sub string, want ...string) {
	h.t.Helper()
	r := h.row(sub)
	for _, w := range want {
		if !strings.Contains(r, w) {
			h.t.Fatalf("row %q lacks %q", r, w)
		}
	}
}

// hasLine reports whether any screen line contains sub.
func (h *harness) hasLine(sub string) bool {
	for _, l := range strings.Split(h.screenText(), "\n") {
		if has(l, sub) {
			return true
		}
	}
	return false
}

// ---- navigation shortcuts ----

// start initialises the app and sizes the terminal. The splash waits for its
// animation tick (a dropped timer) until a key skips it.
func (h *harness) start() {
	h.t.Helper()
	h.send(tea.WindowSizeMsg{Width: termW, Height: termH})
	h.run(h.router.Init())
}

// skipSplash presses a key: "any key skips the animation, never the check".
func (h *harness) skipSplash() { h.t.Helper(); h.key("enter") }

// boot starts the app and moves past the splash.
func (h *harness) boot() { h.t.Helper(); h.start(); h.skipSplash() }

// submitLogin fills the empty login form (focus on the username).
func (h *harness) submitLogin(user, token string) {
	h.t.Helper()
	h.expectTitle("Login")
	h.typeText(user)
	h.key("enter")
	h.typeText(token)
	h.key("enter")
}

// loginOK boots an app that has a config and logs in with the fake's own
// credentials, landing on the menu.
func (h *harness) loginOK() {
	h.t.Helper()
	h.boot()
	h.submitLogin(h.fake.User(), h.fake.Token())
	h.expectTitle("jenktui")
	h.expect("Collections", "Job Catalog", "Quit")
}

// loginNoMenu logs in without expecting the menu, for runs that start on a
// Collection's monitor.
func (h *harness) loginNoMenu() {
	h.t.Helper()
	h.boot()
	h.submitLogin(h.fake.User(), h.fake.Token())
}

// menuTo selects a menu entry (0 Collections, 1 Job Catalog, 2 Overview) and opens it.
func (h *harness) menuTo(index int) {
	h.t.Helper()
	h.expectTitle("jenktui")
	for i := 0; i < index; i++ {
		h.key("down")
	}
	h.key("enter")
}

// openCollections opens the Collections list (titled "Collections", plus
// " · parent" when a parent job is set).
func (h *harness) openCollections() {
	h.t.Helper()
	h.menuTo(0)
	if t := h.title(); t != "Collections" && !strings.HasPrefix(t, "Collections · ") {
		h.t.Fatalf("not on the Collections list: %q", t)
	}
}

func (h *harness) openCatalog() {
	h.t.Helper()
	h.menuTo(1)
	if !strings.HasPrefix(h.title(), "Job Catalog") {
		h.t.Fatalf("not on the Job Catalog: %q", h.title())
	}
}

// openCollection opens the Collection under the cursor on the Collections list.
func (h *harness) openCollection(name string) {
	h.t.Helper()
	h.key("enter")
	if t := h.title(); t != "Collection · "+name && !(strings.HasPrefix(t, "Collection · ") && strings.HasSuffix(t, " · "+name)) {
		h.t.Fatalf("title = %q, want Collection · [parent · ]%s", t, name)
	}
}

// ---- files ----

func (h *harness) path(name string) string { return filepath.Join(h.dir, name) }

// ---- fake helpers ----

// lastQueueID returns the queue id of job's most recent trigger.
func lastQueueID(t *testing.T, f *jenkinstest.Server, job string) int {
	t.Helper()
	ts := f.Triggers()
	for i := len(ts) - 1; i >= 0; i-- {
		if ts[i].Job == job {
			return ts[i].QueueID
		}
	}
	t.Fatalf("job %q was never triggered; triggers: %+v", job, ts)
	return 0
}

// triggerFor returns the single recorded trigger of job.
func triggerFor(t *testing.T, f *jenkinstest.Server, job string) jenkinstest.Trigger {
	t.Helper()
	var found []jenkinstest.Trigger
	for _, tr := range f.Triggers() {
		if tr.Job == job {
			found = append(found, tr)
		}
	}
	if len(found) != 1 {
		t.Fatalf("job %q triggered %d times, want 1: %+v", job, len(found), f.Triggers())
	}
	return found[0]
}

func now() time.Time { return time.Now() }

// vals builds url.Values from alternating name, value pairs.
func vals(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}
