package core

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// chromeHeight is the rows the Router reserves: title bar and help bar.
const chromeHeight = 2

// Router is the root tea.Model. It owns a navigation stack and renders the
// top screen between a title bar and a help bar.
type Router struct {
	Session       *Session
	Theme         Theme
	Help          Help
	stack         []Screen
	width, height int
}

// NewRouter returns a Router showing root.
func NewRouter(s *Session, root Screen) *Router {
	return &Router{Session: s, Theme: NewTheme(), Help: NewHelp(), stack: []Screen{root}}
}

// Top returns the active screen.
func (r *Router) Top() Screen { return r.stack[len(r.stack)-1] }

// Depth returns the stack size.
func (r *Router) Depth() int { return len(r.stack) }

func (r *Router) bodySize() tea.WindowSizeMsg {
	return tea.WindowSizeMsg{Width: r.width, Height: max(r.height-chromeHeight, 0)}
}

// enter initialises s and tells it the current body size.
func (r *Router) enter(s Screen) (Screen, tea.Cmd) {
	cmds := []tea.Cmd{s.Init()}
	if r.width > 0 {
		var c tea.Cmd
		s, c = s.Update(r.bodySize())
		cmds = append(cmds, c)
	}
	return s, tea.Batch(cmds...)
}

func (r *Router) setTop(s Screen) { r.stack[len(r.stack)-1] = s }

func (r *Router) Init() tea.Cmd { return r.Top().Init() }

func (r *Router) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = m.Width, m.Height
		s, c := r.Top().Update(r.bodySize())
		r.setTop(s)
		return r, c
	case tea.KeyMsg:
		if m.String() == "ctrl+c" {
			return r, tea.Quit
		}
	case Quit:
		return r, tea.Quit
	case Push:
		s, c := r.enter(m.Screen)
		r.stack = append(r.stack, s)
		return r, c
	case Replace:
		s, c := r.enter(m.Screen)
		r.setTop(s)
		return r, c
	case Pop:
		if len(r.stack) > 1 {
			r.stack = r.stack[:len(r.stack)-1]
		}
		return r, nil
	}
	s, c := r.Top().Update(msg)
	r.setTop(s)
	return r, c
}

func (r *Router) View() string {
	if v, small := TooSmall(r.width, r.height); small {
		return v
	}
	top := r.Top()
	header := r.Theme.Title.Render(top.Title())
	help := r.Help.View(top.Keys(), r.width)
	body := lipgloss.NewStyle().Height(r.bodySize().Height).MaxHeight(r.bodySize().Height).Render(top.View())
	return strings.Join([]string{header, body, help}, "\n")
}
