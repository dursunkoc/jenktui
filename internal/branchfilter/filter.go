package branchfilter

import (
	"sort"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

// Filter is the Branch Filter: glob Patterns plus an on/off switch.
type Filter struct {
	Patterns []string
	Enabled  bool
}

// Active reports whether the filter hides anything: it must be on and have
// patterns. A filter with no patterns shows every branch.
func (f Filter) Active() bool { return f.Enabled && len(f.Patterns) > 0 }

// Match reports whether a decoded branch name passes the filter.
func (f Filter) Match(branch string) bool {
	if !f.Active() {
		return true
	}
	for _, p := range f.Patterns {
		if MatchPattern(p, branch) {
			return true
		}
	}
	return false
}

// Item is one row after filtering: either a Job, or a placeholder for a
// Multibranch Project that has no (matching) branches.
type Item struct {
	Job         jenkins.Job // zero for placeholders
	Project     string      // full path of the project, for placeholders
	Placeholder bool
	Label       string // display text, e.g. "team/app (no matching branches)"
}

// Key is the unique row key: the job's full name, or the project path.
func (i Item) Key() string {
	if i.Placeholder {
		return i.Project
	}
	return i.Job.FullName
}

// Apply filters branch Jobs of Multibranch Projects and leaves every other Job
// untouched. Each project in projects that ends up with no visible branch is
// represented by a placeholder Item. Result is sorted by Key.
func (f Filter) Apply(jobs []jenkins.Job, projects []string) []Item {
	items := make([]Item, 0, len(jobs))
	shown := map[string]bool{}
	for _, j := range jobs {
		if j.Multibranch != "" {
			if !f.Match(j.Branch) {
				continue
			}
			shown[j.Multibranch] = true
		}
		items = append(items, Item{Job: j, Label: j.FullName})
	}
	label := " (no branches)"
	if f.Active() {
		label = " (no matching branches)"
	}
	for _, p := range projects {
		if !shown[p] {
			items = append(items, Item{Project: p, Placeholder: true, Label: p + label})
		}
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].Key() < items[b].Key() })
	return items
}
