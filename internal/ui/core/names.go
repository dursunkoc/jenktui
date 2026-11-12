package core

import (
	"net/url"
	"strings"
)

// RelName returns a job's full name relative to the Parent Job, for display:
// "team/app/build" under parent "team" is "app/build". A name outside the
// parent (or an empty parent, meaning the Jenkins root) is returned whole,
// so a job never loses the part that tells it apart. Keys, filters and
// saved Collections keep using the full name. A Multibranch branch segment
// (Jenkins forbids '%' in job names, so a '%' means URL-encoded) is shown
// decoded: "app/feature%2Fx" is "app/feature/x".
func RelName(full, parent string) string {
	parent = strings.Trim(parent, "/")
	rel := full
	if rest, ok := strings.CutPrefix(full, parent+"/"); parent != "" && ok && rest != "" {
		rel = rest
	}
	return decodeSegments(rel)
}

// decodeSegments decodes the path segments that hold a '%'.
func decodeSegments(p string) string {
	if !strings.Contains(p, "%") {
		return p
	}
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if strings.Contains(seg, "%") {
			if d, err := url.PathUnescape(seg); err == nil {
				parts[i] = d
			}
		}
	}
	return strings.Join(parts, "/")
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
