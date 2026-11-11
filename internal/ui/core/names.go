package core

import "strings"

// RelName returns a job's full name relative to the Parent Job, for display:
// "team/app/build" under parent "team" is "app/build". A name outside the
// parent (or an empty parent, meaning the Jenkins root) is returned whole,
// so a job never loses the part that tells it apart. Keys, filters and
// saved Collections keep using the full name.
func RelName(full, parent string) string {
	parent = strings.Trim(parent, "/")
	if parent == "" {
		return full
	}
	if rel, ok := strings.CutPrefix(full, parent+"/"); ok && rel != "" {
		return rel
	}
	return full
}

// BranchSep separates a Multibranch Project from its branch in a job name.
const BranchSep = " › "

// JobName is how a job is shown. For a branch of a Multibranch Project, Name
// is the project and Branch the decoded branch name ("feature/x"); for any
// other job Branch is empty.
type JobName struct {
	Name, Branch string
}

// String is the plain name, e.g. "digital-ui › develop".
func (n JobName) String() string {
	if n.Branch == "" {
		return n.Name
	}
	return n.Name + BranchSep + n.Branch
}

// Render draws the name with the separator in the muted colour.
func (n JobName) Render(th Theme) string {
	if n.Branch == "" {
		return n.Name
	}
	return n.Name + th.MutedTxt.Render(BranchSep) + n.Branch
}
