package collections

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/widgets"
)

// topKey is the destination row for the top level; it can never be a folder
// path, because paths hold no leading separator.
const topKey = config.Sep

var keyPick = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose"))

// destScreen picks a folder, for moving or copying into. Folders in blocked
// are shown dimmed and cannot be chosen (the folder being moved, and what
// lies under it).
type destScreen struct {
	*env
	title  string
	list   *widgets.List
	onPick func(path string) error // "" is the top level
	notice string
	w, h   int
}

func newDest(e *env, title string, allowTop bool, blocked map[string]bool, onPick func(string) error) *destScreen {
	s := &destScreen{env: e, title: title, onPick: onPick, w: 80, h: 20,
		list: widgets.NewList(e.theme, widgets.Options{NoSelect: true})}
	var items []widgets.Item
	if allowTop {
		items = append(items, widgets.Item{Key: topKey, Label: "(top level)"})
	}
	if cs, err := config.LoadCollections(e.deps.Path); err == nil {
		for _, p := range cs.Paths() {
			n := strings.Count(p, config.Sep)
			name := p[strings.LastIndex(p, config.Sep)+1:]
			items = append(items, widgets.Item{Key: p, Label: strings.Repeat("  ", n) + name, Disabled: blocked[p]})
		}
	} else {
		s.notice = err.Error()
	}
	s.list.SetItems(items)
	return s
}

func (s *destScreen) Init() tea.Cmd { return nil }

func (s *destScreen) Title() string { return s.title }

func (s *destScreen) Keys() []key.Binding {
	if s.list.Filtering() {
		return s.list.FilterEditKeys()
	}
	return []key.Binding{keyPick, core.KeyFilter, core.KeyBack}
}

func (s *destScreen) Subtitle() string { return s.notice }

func (s *destScreen) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = m.Width, m.Height
		s.list.SetSize(s.w, max(s.h-1, 1))
	case tea.KeyMsg:
		if s.list.Filtering() {
			return s, s.list.Update(m)
		}
		switch {
		case key.Matches(m, core.KeyBack):
			return s, core.Cmd(core.Pop{})
		case key.Matches(m, keyPick):
			it, ok := s.list.Cursor()
			if !ok {
				return s, nil
			}
			if it.Disabled {
				s.notice = "cannot choose this folder"
				return s, nil
			}
			dest := it.Key
			if dest == topKey {
				dest = ""
			}
			if err := s.onPick(dest); err != nil {
				s.notice = err.Error()
				return s, nil
			}
			return s, core.Cmd(core.Pop{})
		}
		return s, s.list.Update(m)
	}
	return s, nil
}

func (s *destScreen) View() string { return s.list.View() }
