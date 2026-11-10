package entry

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// Menu is the main menu: Collections, Job Catalog, Quit.
type Menu struct {
	sess          *core.Session
	deps          Deps
	th            core.Theme
	user          string
	cursor        int
	width, height int
}

var menuItems = []string{"Collections", "Job Catalog", "Quit"}

// NewMenu returns the main menu; user is shown as "signed in as".
func NewMenu(sess *core.Session, deps Deps, user string) *Menu {
	return &Menu{sess: sess, deps: deps, th: core.NewTheme(), user: user}
}

func (m *Menu) Init() tea.Cmd { return nil }

func (m *Menu) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case tea.KeyMsg:
		switch {
		case key.Matches(v, core.KeyUp):
			m.cursor = (m.cursor + len(menuItems) - 1) % len(menuItems)
		case key.Matches(v, core.KeyDown):
			m.cursor = (m.cursor + 1) % len(menuItems)
		case key.Matches(v, core.KeyQuit):
			return m, core.Cmd(core.Quit{})
		case key.Matches(v, core.KeyEnter):
			return m, m.activate()
		}
	}
	return m, nil
}

func (m *Menu) activate() tea.Cmd {
	switch m.cursor {
	case 0:
		return core.Cmd(core.Push{Screen: build(m.deps.Collections, m.sess, "Collections")})
	case 1:
		return core.Cmd(core.Push{Screen: build(m.deps.Catalog, m.sess, "Job Catalog")})
	}
	return core.Cmd(core.Quit{})
}

func build(f Factory, s *core.Session, title string) core.Screen {
	if f == nil {
		return ComingSoon(title)
	}
	return f(s)
}

func (m *Menu) View() string {
	var lines []string
	for i, it := range menuItems {
		if i == m.cursor {
			lines = append(lines, m.th.Selected.Render("> "+it))
		} else {
			lines = append(lines, "  "+it)
		}
	}
	body := m.th.MutedTxt.Render("Signed in as "+m.user) + "\n\n" + strings.Join(lines, "\n")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.th.Box.Render(body))
}

func (m *Menu) Title() string { return "jenktui" }
func (m *Menu) Keys() []key.Binding {
	return []key.Binding{core.KeyUp, core.KeyDown, core.KeyEnter, core.KeyQuit}
}
