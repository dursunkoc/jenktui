package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/sysint"
	"github.com/dursunkoc/jenktui/internal/ui/catalog"
	"github.com/dursunkoc/jenktui/internal/ui/collections"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/entry"
	"github.com/dursunkoc/jenktui/internal/ui/monitor"
	"github.com/dursunkoc/jenktui/internal/ui/overview"
	"github.com/dursunkoc/jenktui/internal/ui/palette"
	"github.com/dursunkoc/jenktui/internal/ui/trigger"
	"github.com/dursunkoc/jenktui/internal/ui/widgets"
	"github.com/dursunkoc/jenktui/internal/version"
)

var _ monitor.Sys = sysint.Sys{}

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "jenktui is a terminal UI for Jenkins.\n\nUsage: jenktui [--version] [collection]\n(flags go before the collection name)\n\nWith a Collection name (any letter case), jenktui opens that Collection's Run Monitor right after login. An unknown name shows the menu with a notice.\n\nSettings live in ~/.config/jenktui/config.toml (set JENKTUI_CONFIG_DIR to change the directory).\n")
	}
	flag.Parse()
	if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println(version.String())
		return
	}

	dir, err := config.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sess := &core.Session{StartCollection: strings.TrimSpace(flag.Arg(0))}
	core.ApplyColorEnv()
	p := tea.NewProgram(core.NewRouter(sess, entry.NewSplash(sess, newDeps(dir))), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newDeps wires the screens together: Menu -> Catalog / Collections -> Trigger -> the Collection's Run Monitor.
func newDeps(dir string) entry.Deps {
	configPath := filepath.Join(dir, "config.toml")

	branchSettings := func(s *core.Session) catalog.Settings {
		return catalog.Settings{
			Patterns: s.Config.BranchFilter,
			Save: func(patterns []string) error {
				cfg := s.Config
				cfg.BranchFilter = patterns
				if err := config.Save(configPath, &cfg); err != nil {
					return err
				}
				s.Config.BranchFilter = patterns
				return nil
			},
		}
	}

	// The session holds the logged-in *jenkins.Client behind core.JenkinsClient.
	client := func(s *core.Session) *jenkins.Client {
		c, _ := s.Client.(*jenkins.Client)
		return c
	}

	// jobURL builds a job's web URL from the configured base URL (never from
	// the URL Jenkins reports); nil without a real client, hiding `o`.
	jobURL := func(s *core.Session) func(string) string {
		if c := client(s); c != nil {
			return c.JobURL
		}
		return nil
	}

	collectionsPath := filepath.Join(dir, "collections.toml")
	sys := sysint.New(os.Stdout)

	// Each Collection has its own Run Monitor for the session, keyed by the
	// lower-cased name (config.Collections.Find is case-insensitive). Leaving
	// a monitor keeps its state; opening the Collection's monitor again, or
	// triggering from the Collection, reuses it.
	runsOf := func(started []trigger.Started) []monitor.Run {
		runs := make([]monitor.Run, len(started))
		for i, st := range started {
			runs[i] = monitor.Run{Job: st.Job, Ref: st.Ref, Err: st.Err}
		}
		return runs
	}
	monitors := map[string]core.Screen{}
	// How each monitor shows its jobs, for the rerun form's title. Keyed by
	// the screen so a rename does not lose it.
	monitorNames := map[core.Screen]map[string]core.JobName{}
	monitorFor := func(s *core.Session, name string) core.Screen {
		k := strings.ToLower(name)
		if m, ok := monitors[k]; ok {
			return m
		}
		var m core.Screen
		rerun := func(job string, params map[string]string) core.Screen {
			return trigger.New(trigger.Deps{
				Client:  client(s),
				Names:   monitorNames[m],
				Parent:  s.Config.ParentJob,
				Prefill: params,
				// The monitor is already on the stack under the form; the new
				// build joins it and the router drops the form.
				Next: func(started []trigger.Started) core.Screen {
					monitor.Add(m, runsOf(started))
					return m
				},
			}, []string{job})
		}
		m = monitor.NewCollection(monitor.Deps{Client: client(s), Parent: s.Config.ParentJob, Sys: sys, Notify: s.Config.NotifyMode(), Rerun: rerun}, name, nil, nil)
		monitors[k] = m
		return m
	}
	// A Collection renamed in the Collections screen keeps its monitor under
	// the new name.
	renamed := func(oldName, newName string) {
		ok, nk := strings.ToLower(oldName), strings.ToLower(newName)
		if m, found := monitors[ok]; found {
			delete(monitors, ok)
			monitors[nk] = m
			monitor.Rename(m, newName)
		}
	}
	// A deleted Collection's monitor is dropped, so a new Collection with the
	// same name does not inherit its rows.
	deleted := func(name string) {
		k := strings.ToLower(name)
		if m, ok := monitors[k]; ok {
			delete(monitorNames, m)
			delete(monitors, k)
		}
	}
	openMonitor := func(s *core.Session) func(string, []string, map[string]bool, map[string]core.JobName) core.Screen {
		return func(name string, jobKeys []string, missing map[string]bool, names map[string]core.JobName) core.Screen {
			_, existed := monitors[strings.ToLower(name)]
			m := monitorFor(s, name)
			monitor.SetJobs(m, jobKeys, missing)
			monitor.SetNames(m, names)
			monitorNames[m] = names
			if !existed { // a kept monitor keeps the user's show/hide toggle
				monitor.HideSuccess(m, true)
			}
			return m
		}
	}

	triggerFor := func(s *core.Session) func(string, []string, map[string]core.JobName) core.Screen {
		return func(collection string, jobKeys []string, names map[string]core.JobName) core.Screen {
			return trigger.New(trigger.Deps{
				Client: client(s),
				Names:  names,
				Parent: s.Config.ParentJob,
				Next: func(started []trigger.Started) core.Screen {
					runs := runsOf(started)
					m := monitorFor(s, collection)
					if cs, err := config.LoadCollections(collectionsPath); err == nil {
						if i := cs.Find(collection); i >= 0 {
							monitor.SetJobs(m, cs.Items[i].Jobs, nil)
						}
					}
					monitor.SetNames(m, names)
					monitorNames[m] = names
					monitor.Add(m, runs)
					// Forced on purpose, even for a kept monitor: it focuses the
					// outcome of the new builds. They are queued or running, so they
					// stay visible until they finish successfully.
					monitor.HideSuccess(m, true)
					return m
				},
			}, jobKeys)
		}
	}

	// resolveCollection finds the Collection (case-insensitive) and reads the
	// catalog to flag missing members and name Multibranch branches. It blocks,
	// so run it in a command; the returned closure builds the monitor and must
	// run on the UI goroutine. An empty closure with a notice means it failed.
	resolveCollection := func(s *core.Session, name string) (func() core.Screen, string) {
		cs, err := config.LoadCollections(collectionsPath)
		if err != nil {
			return nil, "cannot read Collections: " + err.Error()
		}
		i := cs.Find(name)
		if i < 0 {
			return nil, fmt.Sprintf("no Collection named %q", name)
		}
		c := cs.Items[i]
		var missing map[string]bool
		names := map[string]core.JobName{}
		if cl := client(s); cl != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if cat, err := cl.ListCatalog(ctx, s.Config.ParentJob); err == nil {
				names, missing = catalog.NamesOf(c.Jobs, cat, s.Config.ParentJob), catalog.MissingOf(c.Jobs, cat)
			}
		}
		return func() core.Screen {
			return openMonitor(s)(c.Name, slices.Clone(c.Jobs), missing, names)
		}, ""
	}
	openByName := func(s *core.Session, name string) tea.Cmd {
		return func() tea.Msg {
			scr, notice := resolveCollection(s, name)
			return entry.Start{Screen: scr, Notice: notice}
		}
	}

	newCatalog := func(s *core.Session, extra ...catalog.Option) core.Screen {
		opts := append([]catalog.Option{catalog.WithBranchFilter(branchSettings(s)), catalog.WithOpener(sys), catalog.WithJobURL(jobURL(s))}, extra...)
		return catalog.New(s.Client, s.Config.ParentJob, opts...)
	}

	return entry.Deps{
		ConfigDir:      dir,
		OpenCollection: openByName,
		Overview: func(s *core.Session) core.Screen {
			return overview.New(overview.Deps{
				Client: client(s),
				Load: func() ([]overview.Collection, error) {
					cs, err := config.LoadCollections(collectionsPath)
					if err != nil {
						return nil, err
					}
					out := make([]overview.Collection, len(cs.Items))
					for i, c := range cs.Items {
						out[i] = overview.Collection{Name: c.Name, Jobs: c.Jobs}
					}
					return out, nil
				},
				Open: func(name string) tea.Cmd {
					return func() tea.Msg {
						scr, notice := resolveCollection(s, name)
						return overview.Opened{Screen: scr, Notice: notice}
					}
				},
			})
		},
		Catalog: func(s *core.Session) core.Screen { return newCatalog(s) },
		Palette: func(s *core.Session) core.Screen {
			return palette.New(palette.Deps{
				Parent: s.Config.ParentJob,
				Collections: func() ([]string, error) {
					cs, err := config.LoadCollections(collectionsPath)
					if err != nil {
						return nil, err
					}
					names := make([]string, len(cs.Items))
					for i, c := range cs.Items {
						names[i] = c.Name
					}
					return names, nil
				},
				Jobs: func() ([]jenkins.Job, error) {
					cl := client(s)
					if cl == nil {
						return nil, nil
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cat, err := cl.ListCatalog(ctx, s.Config.ParentJob)
					return cat.Jobs, err
				},
				OpenCollection: func(name string) tea.Cmd {
					return func() tea.Msg {
						scr, notice := resolveCollection(s, name)
						return palette.Opened{Screen: scr, Notice: notice}
					}
				},
				OpenJob: func(fullName string) core.Screen {
					return newCatalog(s, catalog.WithFilter(widgets.QuoteGlob(fullName)))
				},
			})
		},
		Collections: func(s *core.Session) core.Screen {
			return collections.New(collections.Deps{
				Lister:       s.Client,
				Parent:       s.Config.ParentJob,
				Path:         collectionsPath,
				Trigger:      triggerFor(s),
				Monitor:      openMonitor(s),
				Renamed:      renamed,
				Deleted:      deleted,
				Opener:       sys,
				JobURL:       jobURL(s),
				BranchFilter: branchSettings(s),
			})
		},
	}
}
