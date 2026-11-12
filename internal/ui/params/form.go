// Package params provides a reusable build-parameter form (a sub-model, not a
// core.Screen) with string, text, bool, choice and password fields.
package params

import (
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// Options tunes a Form.
type Options struct {
	// Prefill overrides a parameter's default value by name.
	Prefill map[string]string
	// Placeholder is shown for an empty value (for choices: the empty choice).
	Placeholder map[string]string
	// Hint is an extra line shown under the title.
	Hint string
}

type field struct {
	def     jenkins.ParamDef
	input   textinput.Model // string, text, password, other
	boolVal bool
	choice  int
	ph      string
}

// Form is a vertical list of parameter fields. Tab/Shift+Tab/Up/Down move,
// Enter moves on and, on the last field, submits. Esc is left to the owner.
// Password values are masked in View and never logged.
type Form struct {
	title         string
	hint          string
	fields        []*field
	focus         int
	done          bool
	th            core.Theme
	width, height int
}

// NewForm builds a form for defs.
func NewForm(title string, defs []jenkins.ParamDef, opts Options) *Form {
	f := &Form{title: title, hint: opts.Hint, th: core.NewTheme(), width: 80, height: 20}
	for _, d := range defs {
		fd := &field{def: d, ph: opts.Placeholder[d.Name]}
		val := d.Default
		if v, ok := opts.Prefill[d.Name]; ok {
			val = v
		}
		switch d.Type {
		case "bool":
			fd.boolVal = strings.EqualFold(val, "true")
		case "choice":
			fd.choice = max(slices.Index(d.Choices, val), 0)
		default:
			in := textinput.New()
			in.Prompt, in.CharLimit, in.Width = "", 4096, 40
			in.Placeholder = fd.ph
			in.PlaceholderStyle, in.CompletionStyle = f.th.Placeholder, f.th.Placeholder
			in.SetValue(val)
			if d.Type == "password" {
				in.EchoMode, in.EchoCharacter = textinput.EchoPassword, '•'
			}
			fd.input = in
		}
		f.fields = append(f.fields, fd)
	}
	f.setFocus(0)
	return f
}

// Len returns the number of fields.
func (f *Form) Len() int { return len(f.fields) }

// SetSize sets the available body size.
func (f *Form) SetSize(w, h int) {
	f.width, f.height = w, h
	for _, fd := range f.fields {
		fd.input.Width = max(min(w-8, 60), 10)
	}
}

// setFocus moves focus to field i (wrapping) and returns the command that
// starts the new text field's cursor blinking.
func (f *Form) setFocus(i int) tea.Cmd {
	if len(f.fields) == 0 {
		return nil
	}
	var cmd tea.Cmd
	f.focus = (i + len(f.fields)) % len(f.fields)
	for k, fd := range f.fields {
		if fd.isText() {
			if k == f.focus {
				cmd = fd.input.Focus()
			} else {
				fd.input.Blur()
			}
		}
	}
	return cmd
}

func (fd *field) isText() bool { return fd.def.Type != "bool" && fd.def.Type != "choice" }

func (fd *field) value() string {
	switch fd.def.Type {
	case "bool":
		if fd.boolVal {
			return "true"
		}
		return "false"
	case "choice":
		if fd.choice < len(fd.def.Choices) {
			return fd.def.Choices[fd.choice]
		}
		return ""
	}
	return fd.input.Value()
}

// Values returns the current value of every field by parameter name.
func (f *Form) Values() map[string]string {
	m := make(map[string]string, len(f.fields))
	for _, fd := range f.fields {
		m[fd.def.Name] = fd.value()
	}
	return m
}

// Done reports whether the user submitted the form.
func (f *Form) Done() bool { return f.done }

// Reopen clears the submitted flag so the form can be edited again.
func (f *Form) Reopen() { f.done = false }

// Init returns the command that starts the focused field's cursor blinking.
// Run it whenever the form is shown; the blink messages that follow reach the
// focused field through Update.
func (f *Form) Init() tea.Cmd { return textinput.Blink }

// Update handles a message and returns a command.
func (f *Form) Update(msg tea.Msg) tea.Cmd {
	if len(f.fields) == 0 {
		return nil
	}
	cur := f.fields[f.focus]
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "tab", "down":
			return f.setFocus(f.focus + 1)
		case "shift+tab", "up":
			return f.setFocus(f.focus - 1)
		case "enter":
			if f.focus == len(f.fields)-1 {
				f.done = true
				return nil
			}
			return f.setFocus(f.focus + 1)
		}
		switch cur.def.Type {
		case "bool":
			if s := k.String(); s == " " || s == "left" || s == "right" || s == "h" || s == "l" {
				cur.boolVal = !cur.boolVal
			}
			return nil
		case "choice":
			n := len(cur.def.Choices)
			if n == 0 {
				return nil
			}
			switch k.String() {
			case "right", "l", " ":
				cur.choice = (cur.choice + 1) % n
			case "left", "h":
				cur.choice = (cur.choice + n - 1) % n
			}
			return nil
		}
	}
	if cur.isText() {
		var cmd tea.Cmd
		cur.input, cmd = cur.input.Update(msg)
		return cmd
	}
	return nil
}

// Masked renders a value for display: password values become ••••.
func Masked(typ, v string) string {
	if typ == "password" && v != "" {
		return "••••"
	}
	return v
}

func (fd *field) render(th core.Theme, focused bool) []string {
	label := fd.def.Name
	if focused {
		label = th.Selected.Render("› " + label)
	} else {
		label = "  " + label
	}
	lines := []string{label + th.MutedTxt.Render("  ("+fd.def.Type+")")}
	if d := strings.TrimSpace(fd.def.Description); d != "" {
		d = strings.Join(strings.Fields(d), " ")
		lines = append(lines, "    "+th.MutedTxt.Render(d))
	}
	var body string
	switch fd.def.Type {
	case "bool":
		box := "[ ]"
		if fd.boolVal {
			box = "[x]"
		}
		body = box
	case "choice":
		v := fd.value()
		if v == "" {
			v = fd.ph
			if v == "" {
				v = "(unset)"
			}
			v = th.MutedTxt.Render(v)
		}
		body = "‹ " + v + " ›"
	default:
		body = fd.input.View()
	}
	return append(lines, "    "+body)
}

// View renders the form, scrolling so the focused field stays visible.
func (f *Form) View() string {
	head := []string{f.th.Title.Render(f.title)}
	if f.hint != "" {
		head = append(head, f.th.MutedTxt.Render(f.hint))
	}
	head = append(head, "")
	if len(f.fields) == 0 {
		return strings.Join(head, "\n")
	}
	blocks := make([][]string, len(f.fields))
	for i, fd := range f.fields {
		blocks[i] = fd.render(f.th, i == f.focus)
		blocks[i] = append(blocks[i], "")
	}
	avail := max(f.height-len(head), 4)
	start, used := f.focus, len(blocks[f.focus])
	for start > 0 && used+len(blocks[start-1]) <= avail {
		start--
		used += len(blocks[start])
	}
	end := f.focus + 1
	for end < len(blocks) && used+len(blocks[end]) <= avail {
		used += len(blocks[end])
		end++
	}
	lines := head
	if start > 0 {
		lines = append(lines, f.th.MutedTxt.Render("  ↑ more"))
	}
	for _, b := range blocks[start:end] {
		lines = append(lines, b...)
	}
	if end < len(blocks) {
		lines = append(lines, f.th.MutedTxt.Render("  ↓ more"))
	}
	return strings.Join(lines, "\n")
}
