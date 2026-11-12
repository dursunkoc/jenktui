package core

import (
	"reflect"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// chromeHeight is the rows the Router reserves: the title bar, and the
// footer (a rule, then the help bar).
const chromeHeight = 3

// Router is the root tea.Model. It owns a navigation stack and renders the
// top screen between a title bar and a help bar.
type Router struct {
	Session       *Session
	Theme         Theme
	Help          Help
	stack         []Screen
	helpOpen      bool // the help panel of a FullHelper screen is shown
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

// fullHelp returns the top screen's help groups, or nil when it has none
// right now (not a FullHelper, or a text field has focus).
func (r *Router) fullHelp() []HelpGroup {
	if fh, ok := r.Top().(FullHelper); ok {
		return fh.FullHelp()
	}
	return nil
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

// push covers the top screen with s, telling it to pause first.
func (r *Router) push(s Screen) (tea.Model, tea.Cmd) {
	r.helpOpen = false
	if p, ok := r.Top().(Pauser); ok {
		p.Pause()
	}
	s, c := r.enter(s)
	r.stack = append(r.stack, s)
	return r, c
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
		// While the help panel is open it takes every key: esc or ? close
		// it, and nothing reaches the screen underneath.
		if r.helpOpen {
			if key.Matches(m, KeyCloseHelp) {
				r.helpOpen = false
			}
			return r, nil
		}
		if key.Matches(m, KeyHelp) && r.fullHelp() != nil {
			r.helpOpen = true
			return r, nil
		}
		// ctrl+p jumps from any screen. It is not typeable text, so no text
		// field needs it. No palette over the palette, and none before login.
		if key.Matches(m, KeyJump) && r.Session != nil && r.Session.Palette != nil {
			if _, open := r.Top().(PaletteScreen); open {
				return r, nil
			}
			return r.push(r.Session.Palette())
		}
	case Quit:
		return r, tea.Quit
	case Push:
		return r.push(m.Screen)
	case Replace:
		r.helpOpen = false
		for i := len(r.stack) - 2; i >= 0; i-- {
			if sameScreen(r.stack[i], m.Screen) {
				r.stack = r.stack[:i+1]
				// It was paused when covered: a Resumer restarts itself
				// instead of starting over with Init.
				if rs, ok := m.Screen.(Resumer); ok {
					var cmds []tea.Cmd
					if r.width > 0 {
						s, c := m.Screen.Update(r.bodySize())
						r.setTop(s)
						cmds = append(cmds, c)
					}
					return r, tea.Batch(append(cmds, rs.Resume())...)
				}
				break
			}
		}
		s, c := r.enter(m.Screen)
		r.setTop(s)
		return r, c
	case Pop:
		r.helpOpen = false
		if len(r.stack) > 1 {
			r.stack = r.stack[:len(r.stack)-1]
			// The uncovered screen may have missed a resize while covered.
			var cmds []tea.Cmd
			if r.width > 0 {
				s, c := r.Top().Update(r.bodySize())
				r.setTop(s)
				cmds = append(cmds, c)
			}
			if rs, ok := r.Top().(Resumer); ok {
				cmds = append(cmds, rs.Resume())
			}
			return r, tea.Batch(cmds...)
		}
		return r, nil
	}
	s, c := r.Top().Update(msg)
	r.setTop(s)
	return r, c
}

// sameScreen reports whether a and b are the same screen pointer. Screens held
// by value are never "the same", which also keeps == from panicking on
// values that cannot be compared.
func sameScreen(a, b Screen) bool {
	if a == nil || b == nil || reflect.TypeOf(a).Kind() != reflect.Ptr || reflect.TypeOf(b).Kind() != reflect.Ptr {
		return false
	}
	return a == b
}

// header renders the title row: the title, then the screen's Subtitle in
// muted text, on one line. The subtitle is cut with "…" to fit the width,
// and dropped when there is no room for it.
func (r *Router) header(top Screen) string {
	title := r.Theme.Title.Render(ansi.Truncate(top.Title(), r.width, "…"))
	st, ok := top.(Subtitler)
	if !ok {
		return title
	}
	sub := st.Subtitle()
	room := r.width - lipgloss.Width(title) - lipgloss.Width(subtitleGap)
	if sub == "" || room < 4 {
		return title
	}
	return title + subtitleGap + r.Theme.MutedTxt.Render(ansi.Truncate(sub, room, "…"))
}

// subtitleGap separates the title from the subtitle.
const subtitleGap = "   "

func (r *Router) View() string {
	if v, small := TooSmall(r.width, r.height); small {
		return v
	}
	top := r.Top()
	header := r.header(top)
	groups := r.fullHelp()
	bh := r.bodySize().Height
	content := top.View()
	var help string
	if r.helpOpen && groups != nil {
		// The panel sits at the bottom of the body; the screen keeps the rows
		// above it. On a short terminal the panel's last rows are cut.
		panel := r.Help.Panel(groups, r.width)
		keep := max(bh-lipgloss.Height(panel), 0)
		content = panel
		if keep > 0 {
			content = lipgloss.NewStyle().Height(keep).MaxHeight(keep).Render(top.View()) + "\n" + panel
		}
		help = r.Help.View([]key.Binding{KeyCloseHelp}, false, r.width)
	} else {
		help = r.Help.View(top.Keys(), groups != nil, r.width)
	}
	body := lipgloss.NewStyle().Height(bh).MaxHeight(bh).Render(content)
	rule := lipgloss.NewStyle().Foreground(r.Theme.Border).Render(strings.Repeat("─", r.width))
	return strings.Join([]string{header, body, rule, help}, "\n")
}
