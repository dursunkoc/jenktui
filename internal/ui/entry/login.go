package entry

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

type loginResult struct {
	client *jenkins.Client
	name   string
	err    error
}

// Login asks for username and password or API token (memory only) and verifies them.
type Login struct {
	sess          *core.Session
	deps          Deps
	th            core.Theme
	user, token   textinput.Model
	focus         int
	err           error
	msg           string
	busy          bool
	width, height int
}

// NewLogin returns the login screen, username prefilled from config.
func NewLogin(sess *core.Session, deps Deps) *Login {
	u := textinput.New()
	u.Prompt, u.CharLimit, u.Width = "", 128, 40
	u.SetValue(sess.Config.Username)
	u.Focus()
	t := textinput.New()
	t.Prompt, t.CharLimit, t.Width = "", 256, 40
	t.EchoMode, t.EchoCharacter = textinput.EchoPassword, '•'
	l := &Login{sess: sess, deps: deps, th: core.NewTheme(), user: u, token: t}
	if u.Value() != "" {
		l.setFocus(1)
	}
	return l
}

func (l *Login) Init() tea.Cmd { return textinput.Blink }

func (l *Login) setFocus(i int) {
	l.focus = i
	l.user.Blur()
	l.token.Blur()
	if i == 0 {
		l.user.Focus()
	} else {
		l.token.Focus()
	}
}

func (l *Login) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		l.width, l.height = m.Width, m.Height
		return l, nil
	case loginResult:
		l.busy = false
		if m.err != nil {
			l.err = m.err
			return l, nil
		}
		l.sess.Client = m.client
		return l, core.Cmd(core.Replace{Screen: NewMenu(l.sess, l.deps, m.name)})
	case tea.KeyMsg:
		if l.busy {
			return l, nil
		}
		switch m.String() {
		case "esc":
			return l, core.Cmd(core.Quit{})
		case "tab", "shift+tab", "up", "down":
			l.setFocus(1 - l.focus)
			return l, nil
		case "enter":
			if l.focus == 0 {
				l.setFocus(1)
				return l, nil
			}
			return l.submit()
		}
	}
	var cmd tea.Cmd
	if l.focus == 0 {
		l.user, cmd = l.user.Update(msg)
	} else {
		l.token, cmd = l.token.Update(msg)
	}
	return l, cmd
}

func (l *Login) submit() (core.Screen, tea.Cmd) {
	user, tok := strings.TrimSpace(l.user.Value()), l.token.Value()
	l.err = nil
	switch {
	case user == "":
		l.msg = "username is required"
		l.setFocus(0)
		return l, nil
	case tok == "":
		l.msg = "password or API token is required"
		l.setFocus(1)
		return l, nil
	}
	l.msg, l.busy = "", true
	cfg := l.sess.Config
	return l, func() tea.Msg {
		c, err := jenkins.New(jenkins.Options{
			BaseURL: cfg.JenkinsURL, Username: user, Token: tok,
			CACertPath: cfg.CACert, InsecureSkipVerify: cfg.InsecureSkipVerify,
		})
		if err != nil {
			return loginResult{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		name, err := c.Ping(ctx)
		if err != nil {
			return loginResult{err: err}
		}
		if name == "" {
			name = user
		}
		return loginResult{client: c, name: name}
	}
}

func (l *Login) View() string {
	lines := []string{
		l.th.Title.Render("Log in to " + l.sess.Config.JenkinsURL),
		"",
		"Username",
		l.user.View(),
		"",
		"Password or API token (kept in memory only)",
		l.token.View(),
	}
	switch {
	case l.busy:
		lines = append(lines, "", l.th.MutedTxt.Render("signing in..."))
	case l.msg != "":
		lines = append(lines, "", lipgloss.NewStyle().Foreground(l.th.Warn).Render(l.msg))
	}
	body := l.th.Box.Render(strings.Join(lines, "\n"))
	if l.err != nil {
		body = lipgloss.JoinVertical(lipgloss.Left, body, "", l.th.ErrorView(errTitle(l.err), l.err, errHint(l.err)))
	}
	return lipgloss.Place(l.width, l.height, lipgloss.Center, lipgloss.Center, body)
}

func (l *Login) Title() string { return "Login" }
func (l *Login) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next field")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "log in")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "quit")),
	}
}
