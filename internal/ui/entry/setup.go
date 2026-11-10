package entry

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

type setupReach struct{ err error }

// Setup is the first-run prompt for jenkins_url and optional parent_job.
type Setup struct {
	sess          *core.Session
	deps          Deps
	th            core.Theme
	url, parent   textinput.Model
	focus         int
	errMsg        string
	busy          bool
	width, height int
}

// NewSetup returns the first-run setup screen.
func NewSetup(sess *core.Session, deps Deps) *Setup {
	u := textinput.New()
	u.Prompt, u.Placeholder, u.CharLimit, u.Width = "", "https://jenkins.example.com", 256, 44
	u.Focus()
	p := textinput.New()
	p.Prompt, p.Placeholder, p.CharLimit, p.Width = "", "optional, e.g. team/app", 256, 44
	return &Setup{sess: sess, deps: deps, th: core.NewTheme(), url: u, parent: p}
}

func (s *Setup) Init() tea.Cmd { return textinput.Blink }

func (s *Setup) setFocus(i int) {
	s.focus = i
	s.url.Blur()
	s.parent.Blur()
	if i == 0 {
		s.url.Focus()
	} else {
		s.parent.Focus()
	}
}

func (s *Setup) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = m.Width, m.Height
		return s, nil
	case setupReach:
		s.busy = false
		if m.err != nil {
			s.errMsg = errTitle(m.err) + ": " + m.err.Error() + "\n" + errHint(m.err)
			return s, nil
		}
		return s, core.Cmd(core.Replace{Screen: NewLogin(s.sess, s.deps)})
	case tea.KeyMsg:
		if s.busy {
			return s, nil
		}
		switch m.String() {
		case "esc":
			return s, core.Cmd(core.Quit{})
		case "tab", "shift+tab", "up", "down":
			s.setFocus(1 - s.focus)
			return s, nil
		case "enter":
			if s.focus == 0 {
				s.setFocus(1)
				return s, nil
			}
			return s.submit()
		}
	}
	var cmd tea.Cmd
	if s.focus == 0 {
		s.url, cmd = s.url.Update(msg)
	} else {
		s.parent, cmd = s.parent.Update(msg)
	}
	return s, cmd
}

func (s *Setup) submit() (core.Screen, tea.Cmd) {
	cfg := config.Config{JenkinsURL: s.url.Value(), ParentJob: s.parent.Value()}
	if err := config.Save(s.deps.configPath(), &cfg); err != nil {
		s.errMsg = err.Error()
		return s, nil
	}
	s.sess.Config = cfg // Save normalised cfg in place
	s.errMsg, s.busy = "", true
	return s, func() tea.Msg { return setupReach{err: checkReachable(cfg)} }
}

func (s *Setup) View() string {
	lines := []string{
		s.th.Title.Render("Welcome - first-run setup"),
		s.th.MutedTxt.Render("No config found. It will be saved to " + s.deps.configPath()),
		"",
		"Jenkins URL",
		s.url.View(),
		"",
		"Parent job (optional)",
		s.parent.View(),
	}
	switch {
	case s.busy:
		lines = append(lines, "", s.th.MutedTxt.Render("checking connection..."))
	case s.errMsg != "":
		lines = append(lines, "", lipgloss.NewStyle().Foreground(s.th.Fail).Render(s.errMsg))
	}
	return lipgloss.Place(s.width, s.height, lipgloss.Center, lipgloss.Center, s.th.Box.Render(strings.Join(lines, "\n")))
}

func (s *Setup) Title() string { return "Setup" }
func (s *Setup) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next field")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "quit")),
	}
}
