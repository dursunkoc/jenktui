package entry

import (
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

const tickInterval = 80 * time.Millisecond

var logo = []string{
	`    _            _    _             _ `,
	`   (_) ___ _ __ | | _| |_ _   _ (_)`,
	`   | |/ _ \ '_ \| |/ / __| | | || |`,
	`   | |  __/ | | |   <| |_| |_| || |`,
	`  _/ |\___|_| |_|_|\_\\__|\__,_||_|`,
	` |__/                              `,
}

// tagline sits under the logo and says what the name stands for.
const tagline = "Jenkins Terminal User Interface"

var spinner = []string{"|", "/", "-", "\\"}

type splashPhase int

const (
	phaseChecking splashPhase = iota
	phaseConfigErr
	phaseUnreachable
)

type splashTick struct{}

type checkOutcome int

const (
	outcomeReady checkOutcome = iota
	outcomeNeedSetup
	outcomeConfigErr
	outcomeUnreachable
)

type checkResult struct {
	outcome checkOutcome
	cfg     config.Config
	err     error
}

// Splash is the root screen: animated logo plus the real startup check.
type Splash struct {
	sess          *core.Session
	deps          Deps
	th            core.Theme
	frame         int
	animDone      bool
	result        *checkResult
	phase         splashPhase
	err           error
	width, height int
}

// NewSplash returns the root screen of the app.
func NewSplash(sess *core.Session, deps Deps) *Splash {
	return &Splash{sess: sess, deps: deps, th: core.NewTheme()}
}

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return splashTick{} })
}

// check loads the config and verifies the URL is reachable.
func (s *Splash) check() tea.Msg {
	cfg, err := config.Load(s.deps.configPath())
	switch {
	case errors.Is(err, config.ErrNotFound):
		return checkResult{outcome: outcomeNeedSetup}
	case err != nil:
		return checkResult{outcome: outcomeConfigErr, err: err}
	}
	if err := checkReachable(*cfg); err != nil {
		return checkResult{outcome: outcomeUnreachable, cfg: *cfg, err: err}
	}
	return checkResult{outcome: outcomeReady, cfg: *cfg}
}

func (s *Splash) Init() tea.Cmd { return tea.Batch(tick(), s.check) }

func (s *Splash) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = m.Width, m.Height
	case splashTick:
		s.frame++
		if time.Duration(s.frame)*tickInterval >= s.deps.minSplash() {
			s.animDone = true
		}
		if !s.animDone || s.phase == phaseChecking && s.result == nil {
			return s, tick()
		}
		return s.advance()
	case checkResult:
		s.result = &m
		return s.advance()
	case tea.KeyMsg:
		if s.phase != phaseChecking {
			switch m.String() {
			case "r":
				s.phase, s.result, s.err = phaseChecking, nil, nil
				s.frame, s.animDone = 0, false
				return s, s.Init()
			case "q", "esc":
				return s, core.Cmd(core.Quit{})
			}
			return s, nil
		}
		// Any key skips the animation, never the check.
		if !s.animDone {
			s.animDone = true
			return s.advance()
		}
	}
	return s, nil
}

// advance moves on once both the animation and the check are finished.
func (s *Splash) advance() (core.Screen, tea.Cmd) {
	if !s.animDone || s.result == nil || s.phase != phaseChecking {
		return s, nil
	}
	r := *s.result
	switch r.outcome {
	case outcomeNeedSetup:
		return s, core.Cmd(core.Replace{Screen: NewSetup(s.sess, s.deps)})
	case outcomeConfigErr:
		s.phase, s.err = phaseConfigErr, r.err
	case outcomeUnreachable:
		s.sess.Config = r.cfg
		s.phase, s.err = phaseUnreachable, r.err
	default:
		s.sess.Config = r.cfg
		return s, core.Cmd(core.Replace{Screen: NewLogin(s.sess, s.deps)})
	}
	return s, nil
}

func (s *Splash) View() string {
	var body string
	switch s.phase {
	case phaseConfigErr:
		body = s.th.ErrorView("Invalid configuration", s.err,
			"Fix "+s.deps.configPath()+" and press r to retry, q to quit.")
	case phaseUnreachable:
		body = s.th.ErrorView(errTitle(s.err), s.err, errHint(s.err)+"\n\nPress r to retry, q to quit.")
	default:
		body = s.logoView() + "\n\n" + s.taglineView() + "\n\n" + s.th.MutedTxt.Render(spinner[s.frame%len(spinner)]+" checking configuration...")
	}
	return lipgloss.Place(s.width, s.height, lipgloss.Center, lipgloss.Center, body)
}

func (s *Splash) logoView() string {
	palette := []lipgloss.AdaptiveColor{s.th.Accent, s.th.Running, s.th.OK}
	var sb strings.Builder
	for i, line := range logo {
		for j, r := range []rune(line) {
			c := palette[(j/3+i+s.frame/2)%len(palette)]
			sb.WriteString(lipgloss.NewStyle().Foreground(c).Bold(true).Render(string(r)))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// taglineView centres the tagline on the logo's width, so it stays under the
// logo whatever the terminal width.
func (s *Splash) taglineView() string {
	w := 0
	for _, line := range logo {
		w = max(w, lipgloss.Width(line))
	}
	return s.th.Title.Width(w).Align(lipgloss.Center).Render(tagline)
}

func (s *Splash) Title() string { return "jenktui" }
func (s *Splash) Keys() []key.Binding {
	if s.phase == phaseChecking {
		return []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("any key", "skip animation"))}
	}
	return []key.Binding{key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "retry")), core.KeyQuit}
}
