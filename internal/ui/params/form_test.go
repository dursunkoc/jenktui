package params

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/jenkins"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeText(f *Form, s string) {
	for _, r := range s {
		f.Update(key(string(r)))
	}
}

var defs = []jenkins.ParamDef{
	{Name: "BRANCH", Type: "string", Default: "main", Description: "branch to build"},
	{Name: "DEBUG", Type: "bool", Default: "true"},
	{Name: "ENV", Type: "choice", Default: "qa", Choices: []string{"dev", "qa", "prod"}},
	{Name: "TOKEN", Type: "password"},
}

func TestFormFlow(t *testing.T) {
	f := NewForm("Params", defs, Options{})
	v := f.Values()
	if v["BRANCH"] != "main" || v["DEBUG"] != "true" || v["ENV"] != "qa" || v["TOKEN"] != "" {
		t.Fatalf("defaults: %v", v)
	}
	typeText(f, "-x") // appended to "main"
	f.Update(key("tab"))
	f.Update(key(" ")) // toggle bool
	f.Update(key("tab"))
	f.Update(key("right")) // qa -> prod
	f.Update(key("tab"))
	typeText(f, "s3cret")
	if strings.Contains(f.View(), "s3cret") {
		t.Fatal("password echoed in view")
	}
	if f.Done() {
		t.Fatal("done too early")
	}
	f.Update(key("enter")) // last field submits
	if !f.Done() {
		t.Fatal("not done after enter on last field")
	}
	v = f.Values()
	if v["BRANCH"] != "main-x" || v["DEBUG"] != "false" || v["ENV"] != "prod" || v["TOKEN"] != "s3cret" {
		t.Fatalf("values: %v", v)
	}
	f.Reopen()
	if f.Done() {
		t.Fatal("reopen")
	}
}

func TestEnterAdvancesAndShiftTabBack(t *testing.T) {
	f := NewForm("P", defs, Options{})
	f.Update(key("enter"))
	f.Update(key("enter"))
	if f.focus != 2 {
		t.Fatalf("focus=%d", f.focus)
	}
	f.Update(key("shift+tab"))
	if f.focus != 1 || f.Done() {
		t.Fatalf("focus=%d done=%v", f.focus, f.Done())
	}
}

func TestPrefillAndPlaceholder(t *testing.T) {
	d := []jenkins.ParamDef{{Name: "ENV", Type: "choice", Choices: []string{"", "dev"}}, {Name: "B", Type: "string"}}
	f := NewForm("P", d, Options{Prefill: map[string]string{"B": "pre"}, Placeholder: map[string]string{"ENV": "(differs)"}})
	if f.Values()["B"] != "pre" || f.Values()["ENV"] != "" {
		t.Fatalf("%v", f.Values())
	}
	if !strings.Contains(f.View(), "(differs)") {
		t.Fatal("placeholder missing")
	}
}

func TestScrollKeepsFocusVisible(t *testing.T) {
	var d []jenkins.ParamDef
	for _, n := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		d = append(d, jenkins.ParamDef{Name: "P_" + n, Type: "string"})
	}
	f := NewForm("P", d, Options{})
	f.SetSize(80, 10)
	for range 7 {
		f.Update(key("tab"))
	}
	if v := f.View(); !strings.Contains(v, "P_H") || !strings.Contains(v, "more") {
		t.Fatalf("view:\n%s", v)
	}
}

func TestMasked(t *testing.T) {
	if Masked("password", "x") != "••••" || Masked("string", "x") != "x" || Masked("password", "") != "" {
		t.Fatal("masked")
	}
}

func TestCursorBlinks(t *testing.T) {
	f := NewForm("t", defs, Options{})
	// Init starts the blink of the focused text field.
	msg := f.Init()()
	if cmd := f.Update(msg); cmd == nil {
		t.Fatal("initial blink message did not start the cursor blinking")
	}
	// Moving onto another text field starts its blink; a bool or choice has none.
	if cmd := f.Update(key("tab")); cmd != nil {
		t.Fatal("bool field has no cursor")
	}
	f.Update(key("tab"))
	if cmd := f.Update(key("tab")); cmd == nil {
		t.Fatal("moving onto a text field did not start its cursor blinking")
	}
}
