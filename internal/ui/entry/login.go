package entry

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dursunkoc/jenktui/internal/config"
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
	// remember is the "Remember me" box. locked: the config already holds a
	// username, so the box is ticked and cannot be changed.
	remember, locked bool
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
	l.locked = strings.TrimSpace(sess.Config.Username) != ""
	l.remember = l.locked
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
	switch i {
	case 0:
		l.user.Focus()
	case 1:
		l.token.Focus()
	}
}

// stops is the number of focus stops: the checkbox is skipped when locked.
func (l *Login) stops() int {
	if l.locked {
		return 2
	}
	return 3
}

func (l *Login) step(d int) {
	n := l.stops()
	l.setFocus((l.focus + d + n) % n)
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
		menu := NewMenu(l.sess, l.deps, m.name)
		menu.notice = l.saveUsername()
		if l.deps.Palette != nil {
			sess, f := l.sess, l.deps.Palette
			l.sess.Palette = func() core.Screen { return f(sess) }
		}
		return l, core.Cmd(core.Replace{Screen: menu})
	case tea.KeyMsg:
		if l.busy {
			return l, nil
		}
		switch m.String() {
		case "esc":
			return l, core.Cmd(core.Quit{})
		case "tab", "down":
			l.step(1)
			return l, nil
		case "shift+tab", "up":
			l.step(-1)
			return l, nil
		case " ":
			if l.focus == 2 {
				if !l.locked {
					l.remember = !l.remember
				}
				return l, nil
			}
		case "enter":
			if l.focus == 0 {
				l.setFocus(1)
				return l, nil
			}
			return l.submit()
		}
	}
	var cmd tea.Cmd
	switch l.focus {
	case 0:
		l.user, cmd = l.user.Update(msg)
	case 1:
		l.token, cmd = l.token.Update(msg)
	}
	return l, cmd
}

// saveUsername writes the typed username to config.toml after a good login,
// when "Remember me" is ticked and was not already locked. It returns the
// notice for the menu: empty on success.
func (l *Login) saveUsername() string {
	if !l.remember || l.locked {
		return ""
	}
	user := strings.TrimSpace(l.user.Value())
	save := l.deps.SaveUsername
	if save == nil {
		save = func(u string) error {
			cfg := l.sess.Config
			cfg.Username = u
			return config.Save(l.deps.configPath(), &cfg)
		}
	}
	if err := save(user); err != nil {
		return "Logged in, but the username could not be saved: " + err.Error()
	}
	l.sess.Config.Username = user
	return ""
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

// memoryNote closes the login box: where the secret goes.
const memoryNote = "Your password or API token is kept in memory only and never written to disk. Only your username is saved."

func (l *Login) View() string {
	lines := []string{
		l.th.Title.Render("Log in to " + l.sess.Config.JenkinsURL),
		"",
		"Username",
		l.user.View(),
		"",
		"Password or API token",
		l.token.View(),
		"",
		l.checkbox(),
	}
	if l.locked {
		lines = append(lines, l.th.MutedTxt.Render("Saved in config.toml. Edit the file to forget it."))
	}
	switch {
	case l.busy:
		lines = append(lines, "", l.th.MutedTxt.Render("signing in..."))
	case l.msg != "":
		lines = append(lines, "", l.th.WarnTxt.Render(l.msg))
	}
	// The note sits at the bottom, under a thin rule as wide as the form,
	// muted and wrapped to that width.
	w := 0
	for _, line := range lines {
		w = max(w, lipgloss.Width(line))
	}
	lines = append(lines, "",
		lipgloss.NewStyle().Foreground(l.th.Border).Render(strings.Repeat("─", w)),
		l.th.MutedTxt.Width(w).Render(memoryNote))
	body := l.th.Box.Render(strings.Join(lines, "\n"))
	if l.err != nil {
		body = lipgloss.JoinVertical(lipgloss.Left, body, "", l.th.ErrorView(errTitle(l.err), l.err, errHint(l.err)))
	}
	return lipgloss.Place(l.width, l.height, lipgloss.Center, lipgloss.Center, body)
}

func (l *Login) checkbox() string {
	box := "[ ]"
	if l.remember {
		box = "[x]"
	}
	text := box + " Remember me"
	switch {
	case l.locked:
		return l.th.MutedTxt.Render(text)
	case l.focus == 2:
		return l.th.Title.Render(text)
	}
	return text
}

func (l *Login) Title() string { return "Login" }
func (l *Login) Keys() []key.Binding {
	keys := []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next field")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "log in")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "quit")),
	}
	if !l.locked {
		keys = append(keys, key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "remember me")))
	}
	return keys
}
