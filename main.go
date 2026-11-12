package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/sysint"
	"github.com/dursunkoc/jenktui/internal/ui/catalog"
	"github.com/dursunkoc/jenktui/internal/ui/collections"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/entry"
	"github.com/dursunkoc/jenktui/internal/ui/monitor"
	"github.com/dursunkoc/jenktui/internal/ui/trigger"
	"github.com/dursunkoc/jenktui/internal/version"
)

var _ monitor.Sys = sysint.Sys{}

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "jenktui is a terminal UI for Jenkins.\n\nUsage: jenktui [--version]\n\nSettings live in ~/.config/jenktui/config.toml (set JENKTUI_CONFIG_DIR to change the directory).\n")
	}
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String())
		return
	}

	dir, err := config.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sess := &core.Session{}
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
	monitors := map[string]core.Screen{}
	monitorFor := func(s *core.Session, name string) core.Screen {
		k := strings.ToLower(name)
		if m, ok := monitors[k]; ok {
			return m
		}
		m := monitor.NewCollection(monitor.Deps{Client: client(s), Parent: s.Config.ParentJob, Sys: sys, Notify: s.Config.NotifyMode()}, name, nil, nil)
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
	openMonitor := func(s *core.Session) func(string, []string, map[string]bool, map[string]core.JobName) core.Screen {
		return func(name string, jobKeys []string, missing map[string]bool, names map[string]core.JobName) core.Screen {
			m := monitorFor(s, name)
			monitor.SetJobs(m, jobKeys, missing)
			monitor.SetNames(m, names)
			monitor.HideSuccess(m, true)
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
					runs := make([]monitor.Run, len(started))
					for i, st := range started {
						runs[i] = monitor.Run{Job: st.Job, Ref: st.Ref, Err: st.Err}
					}
					m := monitorFor(s, collection)
					if cs, err := config.LoadCollections(collectionsPath); err == nil {
						if i := cs.Find(collection); i >= 0 {
							monitor.SetJobs(m, cs.Items[i].Jobs, nil)
						}
					}
					monitor.SetNames(m, names)
					monitor.Add(m, runs)
					// Hide successful jobs, as `m` does. The new builds are queued or
					// running, so they stay visible until they finish successfully.
					monitor.HideSuccess(m, true)
					return m
				},
			}, jobKeys)
		}
	}

	return entry.Deps{
		ConfigDir: dir,
		Catalog: func(s *core.Session) core.Screen {
			return catalog.New(s.Client, s.Config.ParentJob, catalog.WithBranchFilter(branchSettings(s)), catalog.WithOpener(sys), catalog.WithJobURL(jobURL(s)))
		},
		Collections: func(s *core.Session) core.Screen {
			return collections.New(collections.Deps{
				Lister:       s.Client,
				Parent:       s.Config.ParentJob,
				Path:         collectionsPath,
				Trigger:      triggerFor(s),
				Monitor:      openMonitor(s),
				Renamed:      renamed,
				Opener:       sys,
				JobURL:       jobURL(s),
				BranchFilter: branchSettings(s),
			})
		},
	}
}
