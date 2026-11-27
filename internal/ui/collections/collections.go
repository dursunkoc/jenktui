// Package collections implements the Collections feature: the Collections
// tree (folders and their jobs, status, reorder, trigger, move and copy), the
// folder picker and the shared Job picker used to edit a folder's jobs.
package collections

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/catalog"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// Deps is everything the Collections screens need; all of it is injected.
type Deps struct {
	// Lister fetches the Job Catalog. If it also implements
	// catalog.CatalogLister (as *jenkins.Client does) Multibranch Projects are
	// reported too; otherwise ListJobs is used.
	Lister catalog.JobLister
	// Parent is the Parent Job ("" means the Jenkins root).
	Parent string
	// Path is the collections.toml file; read and written on every change.
	Path string
	// Trigger builds the screen that triggers builds of the given Job keys
	// (jenkins.Job.FullName, branch names stay URL-encoded). It is pushed by
	// the Collection view's `t`. collection is the Collection's name, so the
	// started builds can be added to that Collection's Run Monitor; names
	// says how the Collection's members are shown there (see Monitor). May be
	// nil (triggering is then unavailable).
	Trigger func(collection string, jobKeys []string, names map[string]core.JobName) core.Screen
	// Monitor returns the Run Monitor screen of a Collection (get-or-create),
	// synced to jobKeys and with the missing members flagged, ready to push
	// with successful rows hidden. Pushed by `m` in the Collections list and
	// in an opened Collection. names says how each known job is shown (it
	// may be nil or lack a job: the monitor then shows the plain name). May
	// be nil (the monitor is then unavailable).
	Monitor func(name string, jobKeys []string, missing map[string]bool, names map[string]core.JobName) core.Screen
	// Renamed, when set, is told after a Collection was renamed so per-name
	// state (such as its Run Monitor) can follow it.
	Renamed func(oldName, newName string)
	// Deleted, when set, is told after a Collection was deleted so per-name
	// state (its Run Monitor) is dropped; a Collection created later under
	// the same name starts fresh.
	Deleted func(name string)
	// Opener opens the highlighted job in the browser (`o` in an opened
	// Collection). May be nil: the key is then hidden.
	Opener catalog.Opener
	// JobURL builds a job's web URL from its full name (the client's JobURL).
	// `o` needs both Opener and JobURL.
	JobURL func(fullName string) string
	// Expanded is which folders are open in the tree, keyed by lower-cased
	// path. main.go keeps one map for the run, so leaving the screen and
	// coming back finds the folders as they were. nil: every visit starts
	// with all folders closed.
	Expanded map[string]bool
	// BranchFilter wires the Branch Filter patterns and their persistence into
	// the picker, exactly as for the Job Catalog.
	BranchFilter catalog.Settings
}

// New returns the Collections screen (the menu entry): one tree of folders
// and their jobs.
func New(deps Deps) core.Screen {
	e := &env{deps: deps, theme: core.NewTheme(), patterns: append([]string(nil), deps.BranchFilter.Patterns...)}
	return newTree(e)
}

// env is state shared by the screens of one Collections session.
type env struct {
	deps     Deps
	theme    core.Theme
	patterns []string // latest Branch Filter patterns, so later pickers see saved edits
}

// titled appends the Parent Job to a title, "Collections · team", so the
// screen says which project its Collections belong to. Under the Jenkins
// root the title is unchanged.
func (e *env) titled(title string) string {
	if p := strings.Trim(e.deps.Parent, "/"); p != "" {
		return title + " · " + p
	}
	return title
}

// pickerTitle is the member picker's title: "Edit Ops", or with a Parent Job
// "Edit · team · Ops", like the Collection screens.
func (e *env) pickerTitle(verb, name string) string {
	if p := strings.Trim(e.deps.Parent, "/"); p != "" {
		return verb + " · " + p + " · " + name
	}
	return verb + " " + name
}

func (e *env) branchSettings() catalog.Settings {
	return catalog.Settings{
		Patterns: append([]string(nil), e.patterns...),
		Save: func(p []string) error {
			e.patterns = append([]string(nil), p...)
			if e.deps.BranchFilter.Save != nil {
				return e.deps.BranchFilter.Save(p)
			}
			return nil
		},
	}
}

// mutate loads the file, applies fn and saves, so concurrent edits are not
// overwritten with stale data.
func (e *env) mutate(fn func(*config.Collections) error) error {
	cs, err := config.LoadCollections(e.deps.Path)
	if err != nil {
		return err
	}
	if err := fn(cs); err != nil {
		return err
	}
	return config.SaveCollections(e.deps.Path, cs)
}

type loadedMsg struct {
	seq int
	cat jenkins.Catalog
	err error
}

// loadCatalog fetches the Job Catalog like internal/ui/catalog does.
func (e *env) loadCatalog(seq int) tea.Cmd {
	l, p := e.deps.Lister, e.deps.Parent
	return func() tea.Msg {
		if cl, ok := l.(catalog.CatalogLister); ok {
			cat, err := cl.ListCatalog(context.Background(), p)
			return loadedMsg{seq: seq, cat: cat, err: err}
		}
		jobs, err := l.ListJobs(context.Background(), p)
		return loadedMsg{seq: seq, cat: jenkins.Catalog{Jobs: jobs}, err: err}
	}
}

// badge maps a Jenkins color to a badge status via jenkins.StatusFromColor.
func badge(color string) core.Status {
	switch jenkins.StatusFromColor(color) {
	case jenkins.JobSuccess:
		return core.StatusSuccess
	case jenkins.JobFailed:
		return core.StatusFailed
	case jenkins.JobUnstable:
		return core.StatusUnstable
	case jenkins.JobAborted:
		return core.StatusAborted
	case jenkins.JobRunning:
		return core.StatusRunning
	}
	return core.StatusUnknown
}

func newInput(placeholder, value string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ph := core.NewTheme().Placeholder
	ti.PlaceholderStyle, ti.CompletionStyle = ph, ph
	ti.CharLimit = 80
	ti.SetValue(value)
	ti.Focus()
	return ti
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
