package catalog

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/branchfilter"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// Settings wires the Branch Filter patterns into a screen.
type Settings struct {
	Patterns []string
	// Save persists the patterns after the editor changed them; may be nil.
	Save func(patterns []string) error
}

// Bindings for the Branch Filter, shared by every screen that browses the
// Job Catalog.
var (
	KeyBranch = key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "branches on/off"))
	KeyEditor = key.NewBinding(key.WithKeys("B"), key.WithHelp("B", "branch filter"))

	keyAdd  = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add"))
	keyDel  = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
	keyDone = key.NewBinding(key.WithKeys("enter", "esc"), key.WithHelp("enter/esc", "close"))
)

// BranchPanel holds the Branch Filter state (patterns, on/off switch) and its
// pattern editor. Embed it in a screen: route keys through HandleKey, draw
// EditorView while Editing, and rebuild the list whenever HandleKey reports a
// change. The zero value is unusable; build it with NewBranchPanel.
type BranchPanel struct {
	theme    core.Theme
	settings Settings
	patterns []string
	filterOn bool

	editing bool   // editor open
	adding  bool   // editor is reading a new pattern
	input   string // pattern being typed
	cursor  int    // selected pattern in the editor
	dirty   bool
	notice  string // last editor/save message
}

// NewBranchPanel returns a panel for st; the filter starts on when there are
// patterns.
func NewBranchPanel(th core.Theme, st Settings) BranchPanel {
	return BranchPanel{theme: th, settings: st,
		patterns: append([]string(nil), st.Patterns...), filterOn: len(st.Patterns) > 0}
}

// Filter returns the current Branch Filter.
func (p *BranchPanel) Filter() branchfilter.Filter {
	return branchfilter.Filter{Patterns: p.patterns, Enabled: p.filterOn}
}

// Editing reports whether the pattern editor is open and owns the keyboard.
func (p *BranchPanel) Editing() bool { return p.editing }

// Notice is the last editor/save message ("" when none).
func (p *BranchPanel) Notice() string { return p.notice }

// Status is a short description of the filter state, e.g. "branches: all".
func (p *BranchPanel) Status() string {
	if !p.Filter().Active() {
		return "branches: all"
	}
	if n := len(p.patterns); n == 1 {
		return "branches: filtered (1 pattern)"
	} else {
		return fmt.Sprintf("branches: filtered (%d patterns)", n)
	}
}

// HandleKey processes the `b` / `B` keys (outside the editor) and, while the
// editor is open, every key. handled says the key was consumed; changed says
// the filter result changed, so the caller must rebuild its rows.
func (p *BranchPanel) HandleKey(m tea.KeyMsg) (handled, changed bool) {
	if p.editing {
		return true, p.editorKey(m)
	}
	switch {
	case key.Matches(m, KeyBranch):
		p.filterOn = !p.filterOn
		p.notice = ""
		return true, true
	case key.Matches(m, KeyEditor):
		p.editing, p.adding, p.cursor, p.notice = true, false, 0, ""
		return true, false
	}
	return false, false
}

// EditorKeys are the help-bar bindings while the editor is open.
func (p *BranchPanel) EditorKeys() []key.Binding {
	if p.adding {
		return []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "add")), key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))}
	}
	return []key.Binding{core.KeyUp, core.KeyDown, keyAdd, keyDel, keyDone}
}

// editorKey handles keys while the editor is open; it reports whether the
// filter result changed (editor closed after edits).
func (p *BranchPanel) editorKey(m tea.KeyMsg) bool {
	if p.adding {
		switch m.Type {
		case tea.KeyEsc:
			p.adding, p.input, p.notice = false, "", ""
		case tea.KeyEnter:
			pat := strings.TrimSpace(p.input)
			if err := branchfilter.Validate(pat); err != nil {
				p.notice = err.Error()
				return false
			}
			p.patterns = append(p.patterns, pat)
			p.cursor, p.dirty = len(p.patterns)-1, true
			p.adding, p.input, p.notice = false, "", ""
		case tea.KeyBackspace:
			if r := []rune(p.input); len(r) > 0 {
				p.input = string(r[:len(r)-1])
			}
		case tea.KeySpace:
			p.input += " "
		case tea.KeyRunes:
			p.input += string(m.Runes)
		}
		return false
	}
	switch {
	case key.Matches(m, keyDone):
		p.closeEditor()
		return true
	case key.Matches(m, core.KeyUp):
		p.cursor = max(p.cursor-1, 0)
	case key.Matches(m, core.KeyDown):
		p.cursor = min(p.cursor+1, max(len(p.patterns)-1, 0))
	case key.Matches(m, keyAdd):
		p.adding, p.input, p.notice = true, "", ""
	case key.Matches(m, keyDel):
		if p.cursor < len(p.patterns) {
			p.patterns = append(p.patterns[:p.cursor:p.cursor], p.patterns[p.cursor+1:]...)
			p.cursor = min(p.cursor, max(len(p.patterns)-1, 0))
			p.dirty = true
		}
	}
	return false
}

// closeEditor saves changed patterns.
func (p *BranchPanel) closeEditor() {
	p.editing, p.notice = false, ""
	if !p.dirty {
		return
	}
	p.dirty = false
	if p.settings.Save != nil {
		if err := p.settings.Save(append([]string(nil), p.patterns...)); err != nil {
			p.notice = "could not save branch filter: " + err.Error()
		}
	}
	if len(p.patterns) > 0 {
		p.filterOn = true // editing implies you want to see its effect
	}
}

// EditorView renders the pattern editor.
func (p *BranchPanel) EditorView() string {
	var b strings.Builder
	b.WriteString(p.theme.Title.Render("Branch Filter") + "\n")
	b.WriteString(p.theme.MutedTxt.Render("Case-sensitive globs on branch names; * matches any text including /. No patterns = all branches.") + "\n\n")
	if len(p.patterns) == 0 {
		b.WriteString(p.theme.MutedTxt.Render("  (no patterns)") + "\n")
	}
	for i, pat := range p.patterns {
		if i == p.cursor && !p.adding {
			b.WriteString(p.theme.Selected.Render("> "+pat) + "\n")
		} else {
			b.WriteString("  " + pat + "\n")
		}
	}
	if p.adding {
		b.WriteString("\n" + p.theme.Selected.Render("new pattern: "+p.input+"█") + "\n")
	}
	if p.notice != "" {
		b.WriteString("\n" + p.theme.ErrTitle.Render(p.notice) + "\n")
	}
	return p.theme.Box.Render(strings.TrimRight(b.String(), "\n"))
}
