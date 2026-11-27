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

// canonicalPath spells a folder path as stored, whatever letter case was asked.
func canonicalPath(cs *config.Collections, path string) string {
	want := strings.ToLower(strings.Trim(path, config.Sep))
	for _, p := range cs.Paths() {
		if strings.ToLower(p) == want {
			return p
		}
	}
	return path
}

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "jenktui is a terminal UI for Jenkins.\n\nUsage: jenktui [--version] [collection]\n(flags go before the collection name)\n\nWith a Collection name (any letter case), jenktui opens that Collection's Run Monitor right after login. A folder inside another is named by its path, such as backend/develop. An unknown name shows the menu with a notice.\n\nSettings live in ~/.config/jenktui/config.toml (set JENKTUI_CONFIG_DIR to change the directory).\n")
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
	p := tea.NewProgram(core.NewRouter(sess, entry.NewSplash(sess, newDeps(dir))), tea.WithAltScreen(), tea.WithReportFocus())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newDeps wires the screens together: Menu -> Catalog / Collections -> Trigger -> the Collection's Run Monitor.
// newSys returns the operating-system integration (browser, clipboard,
// notifications). Tests replace it so they never start a real process.
var newSys = func() sysint.Sys { return sysint.New(os.Stdout) }

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
	presetsPath := filepath.Join(dir, "presets.toml")
	sys := newSys()

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
				Presets: config.PresetFile{Path: presetsPath},
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
	// The monitors of the folders under it follow too, as their paths change.
	renamed := func(oldName, newName string) {
		ok, nk := strings.ToLower(oldName), strings.ToLower(newName)
		for k, m := range monitors {
			if k != ok && !strings.HasPrefix(k, ok+config.Sep) {
				continue
			}
			nk := nk + strings.TrimPrefix(k, ok)
			delete(monitors, k)
			monitors[nk] = m
			monitor.Rename(m, newName+strings.TrimPrefix(k, ok))
		}
	}
	// A deleted Collection's monitor is dropped, so a new Collection with the
	// same name does not inherit its rows.
	// So are the monitors of the folders under it.
	deleted := func(name string) {
		k := strings.ToLower(name)
		for mk, m := range monitors {
			if mk == k || strings.HasPrefix(mk, k+config.Sep) {
				delete(monitorNames, m)
				delete(monitors, mk)
			}
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
				Client:  client(s),
				Names:   names,
				Parent:  s.Config.ParentJob,
				Presets: config.PresetFile{Path: presetsPath},
				Next: func(started []trigger.Started) core.Screen {
					runs := runsOf(started)
					m := monitorFor(s, collection)
					// Builds from jobs no one folder holds go to the Selection monitor,
					// which has no folder to sync from.
					if cs, err := config.LoadCollections(collectionsPath); err == nil && collection != collections.SelectionName {
						if c := cs.Get(collection); c != nil {
							monitor.SetJobs(m, c.Subtree(), nil)
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
		c := cs.Get(name)
		if c == nil {
			return nil, fmt.Sprintf("no Collection named %q", name)
		}
		// The folder's whole subtree; its path, spelled as stored, names the monitor.
		jobs, path := c.Subtree(), canonicalPath(cs, name)
		var missing map[string]bool
		names := map[string]core.JobName{}
		if cl := client(s); cl != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if cat, err := cl.ListCatalog(ctx, s.Config.ParentJob); err == nil {
				names, missing = catalog.NamesOf(jobs, cat, s.Config.ParentJob), catalog.MissingOf(jobs, cat)
			}
		}
		return func() core.Screen {
			return openMonitor(s)(path, slices.Clone(jobs), missing, names)
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

	expanded := map[string]bool{} // the open folders of the Collections tree, kept for the run
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
						out[i] = overview.Collection{Name: c.Name, Jobs: c.Subtree()}
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
					return cs.Paths(), nil
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
				Expanded:     expanded,
				Renamed:      renamed,
				Deleted:      deleted,
				Opener:       sys,
				JobURL:       jobURL(s),
				BranchFilter: branchSettings(s),
			})
		},
	}
}
