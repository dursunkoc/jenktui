// Package core is the UI foundation of jenktui: the Screen contract, the
// navigation Router, the shared Theme, keymap and small view helpers.
package core

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/bubbles/key"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins"
)

// JenkinsClient is the Jenkins API seen by screens; *jenkins.Client satisfies it.
type JenkinsClient interface {
	Ping(ctx context.Context) (string, error)
	ListJobs(ctx context.Context, parent string) ([]jenkins.Job, error)
	JobParameters(ctx context.Context, fullName string) ([]jenkins.ParamDef, error)
}

// Session is shared, in-memory state passed to screens at construction.
// Credentials live only inside Client and are never persisted.
type Session struct {
	Config config.Config
	Client JenkinsClient // nil until login succeeds
}

// Screen is one page of the app. Update returns the (possibly new) Screen
// value, mirroring tea.Model but keeping the stack typed.
type Screen interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (Screen, tea.Cmd)
	// View renders the body only; the Router adds the title bar and help bar.
	View() string
	// Title is shown in the header.
	Title() string
	// Keys lists the bindings shown in the help bar.
	Keys() []key.Binding
}

// Subtitler is optionally implemented by screens that show a short status
// (counts, filter state, notices) on the title row, after the title and in
// muted text. Return "" to show the title alone.
type Subtitler interface {
	Subtitle() string
}

// Navigation messages. Screens return them from commands via Nav* helpers or
// directly as tea.Msg values.
type (
	// Push puts Screen on top of the stack.
	Push struct{ Screen Screen }
	// Pop removes the top screen; a no-op when only one remains.
	Pop struct{}
	// Replace swaps the top screen for Screen.
	Replace struct{ Screen Screen }
	// Quit exits the program.
	Quit struct{}
)

// Cmd wraps msg in a tea.Cmd, for returning navigation from Update.
func Cmd(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }
