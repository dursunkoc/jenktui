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
