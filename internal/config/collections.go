package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Sep separates the names of a folder path: "backend/develop".
const Sep = "/"

// Collection is a folder: a named, ordered group of Jobs (by full path) that
// may also hold sub-folders, to any depth. A top-level folder is what used to
// be a flat Collection, and a file written before folders existed loads as is.
type Collection struct {
	Name    string       `toml:"name"`
	Jobs    []string     `toml:"jobs"`
	Folders []Collection `toml:"folder,omitempty"`
}

// Collections is the persisted tree of folders; Items are the top-level
// folders, in display order. A folder is addressed by its path of names from
// the top, joined with Sep, matched case-insensitively. The root itself is the
// empty path and holds only folders.
type Collections struct {
	Items []Collection `toml:"collection"`
}

// JoinPath builds a folder path from its names.
func JoinPath(names ...string) string { return strings.Join(names, Sep) }

// SplitPath splits a folder path into its names; "" is the root.
func SplitPath(path string) []string {
	path = strings.Trim(strings.TrimSpace(path), Sep)
	if path == "" {
		return nil
	}
	return strings.Split(path, Sep)
}

// ValidName reports why name cannot be a new folder name.
func ValidName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("collection name is required")
	case strings.Contains(name, Sep):
		return fmt.Errorf("collection name cannot contain %q", Sep)
	}
	return nil
}

func indexOf(list []Collection, name string) int {
	for i, c := range list {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}

// Find returns the index of the named top-level folder, or -1.
func (cs *Collections) Find(name string) int { return indexOf(cs.Items, name) }

// siblings returns the folder list that holds the children of parent ("" is
// the top level). A top-level name that itself contains Sep (hand-edited
// before folders existed) is still found by its whole name.
func (cs *Collections) siblings(parent string) (*[]Collection, error) {
	list := &cs.Items
	names := SplitPath(parent)
	if len(names) > 1 {
		if i := cs.Find(strings.Trim(strings.TrimSpace(parent), Sep)); i >= 0 {
			return &cs.Items[i].Folders, nil
		}
	}
	for _, n := range names {
		i := indexOf(*list, n)
		if i < 0 {
			return nil, fmt.Errorf("collection %q not found", parent)
		}
		list = &(*list)[i].Folders
	}
	return list, nil
}

// split separates a folder path into its parent path and last name.
func split(path string) (parent, name string) {
	path = strings.Trim(strings.TrimSpace(path), Sep)
	if i := strings.LastIndex(path, Sep); i >= 0 {
		return path[:i], path[i+1:]
	}
	return "", path
}

// Get returns the folder at path, or nil.
func (cs *Collections) Get(path string) *Collection {
	parent, name := split(path)
	if name == "" {
		return nil
	}
	if i := cs.Find(strings.Trim(strings.TrimSpace(path), Sep)); i >= 0 {
		return &cs.Items[i]
	}
	list, err := cs.siblings(parent)
	if err != nil {
		return nil
	}
	if i := indexOf(*list, name); i >= 0 {
		return &(*list)[i]
	}
	return nil
}

// Paths lists every folder's path, depth first, in display order.
func (cs *Collections) Paths() []string {
	var out []string
	var walk func(prefix string, list []Collection)
	walk = func(prefix string, list []Collection) {
		for _, c := range list {
			p := c.Name
			if prefix != "" {
				p = prefix + Sep + c.Name
			}
			out = append(out, p)
			walk(p, c.Folders)
		}
	}
	walk("", cs.Items)
	return out
}

// Subtree returns the Jobs of the folder and all its descendants, sub-folders
// first as they are listed, each Job once.
func (c *Collection) Subtree() []string {
	seen := map[string]bool{}
	var out []string
	var walk func(c *Collection)
	walk = func(c *Collection) {
		for i := range c.Folders {
			walk(&c.Folders[i])
		}
		for _, j := range c.Jobs {
			if !seen[j] {
				seen[j] = true
				out = append(out, j)
			}
		}
	}
	walk(c)
	return out
}

// CountFolders returns how many folders lie below c, at any depth.
func (c *Collection) CountFolders() int {
	n := len(c.Folders)
	for i := range c.Folders {
		n += c.Folders[i].CountFolders()
	}
	return n
}

// Add appends a new top-level folder. Names must be valid and unique
// (case-insensitive).
func (cs *Collections) Add(name string, jobs []string) error { return cs.AddIn("", name, jobs) }

// AddIn appends a new folder under the folder at parent ("" is the top level).
func (cs *Collections) AddIn(parent, name string, jobs []string) error {
	name = strings.TrimSpace(name)
	if err := ValidName(name); err != nil {
		return err
	}
	list, err := cs.siblings(parent)
	if err != nil {
		return err
	}
	if indexOf(*list, name) >= 0 {
		return fmt.Errorf("collection %q already exists", name)
	}
	*list = append(*list, Collection{Name: name, Jobs: append([]string(nil), jobs...)})
	return nil
}

// Rename changes the name of the folder at path, keeping sibling names unique.
func (cs *Collections) Rename(path, name string) error {
	c := cs.Get(path)
	if c == nil {
		return fmt.Errorf("collection %q not found", path)
	}
	name = strings.TrimSpace(name)
	if err := ValidName(name); err != nil {
		return err
	}
	parent, _ := split(path)
	if cs.Find(strings.Trim(path, Sep)) >= 0 {
		parent = ""
	}
	list, err := cs.siblings(parent)
	if err != nil {
		return err
	}
	if j := indexOf(*list, name); j >= 0 && &(*list)[j] != c {
		return fmt.Errorf("collection %q already exists", name)
	}
	c.Name = name
	return nil
}

// SetJobs replaces a folder's own Jobs (order preserved, duplicates dropped).
func (cs *Collections) SetJobs(path string, jobs []string) error {
	c := cs.Get(path)
	if c == nil {
		return fmt.Errorf("collection %q not found", path)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		if !seen[j] {
			seen[j] = true
			out = append(out, j)
		}
	}
	c.Jobs = out
	return nil
}

// Delete removes the folder at path with everything below it.
func (cs *Collections) Delete(path string) error {
	parent, name := split(path)
	if cs.Find(strings.Trim(strings.TrimSpace(path), Sep)) >= 0 {
		parent, name = "", strings.Trim(strings.TrimSpace(path), Sep)
	}
	list, err := cs.siblings(parent)
	if err != nil {
		return err
	}
	i := indexOf(*list, name)
	if i < 0 {
		return fmt.Errorf("collection %q not found", path)
	}
	*list = append((*list)[:i], (*list)[i+1:]...)
	return nil
}

// MoveJob moves the Job at index from to index to within a folder (reorder).
func (cs *Collections) MoveJob(path string, from, to int) error {
	c := cs.Get(path)
	if c == nil {
		return fmt.Errorf("collection %q not found", path)
	}
	jobs := c.Jobs
	if from < 0 || from >= len(jobs) || to < 0 || to >= len(jobs) {
		return errors.New("index out of range")
	}
	j := jobs[from]
	jobs = append(jobs[:from], jobs[from+1:]...)
	jobs = append(jobs[:to], append([]string{j}, jobs[to:]...)...)
	c.Jobs = jobs
	return nil
}

// MoveFolderBy moves the folder one place up (delta -1) or down (delta +1)
// among its siblings; at either end nothing changes.
func (cs *Collections) MoveFolderBy(path string, delta int) error {
	path = strings.Trim(strings.TrimSpace(path), Sep)
	parent, name := split(path)
	if cs.Find(path) >= 0 {
		parent, name = "", path
	}
	list, err := cs.siblings(parent)
	if err != nil {
		return err
	}
	i := indexOf(*list, name)
	if i < 0 {
		return fmt.Errorf("collection %q not found", path)
	}
	if j := i + delta; j >= 0 && j < len(*list) {
		(*list)[i], (*list)[j] = (*list)[j], (*list)[i]
	}
	return nil
}

// MoveFolder moves the folder at path under the folder at parent ("" is the
// top level) and returns its new path. Moving a folder into itself or one of
// its descendants, or onto a sibling name already taken, is refused.
func (cs *Collections) MoveFolder(path, parent string) (string, error) {
	path = strings.Trim(strings.TrimSpace(path), Sep)
	parent = strings.Trim(strings.TrimSpace(parent), Sep)
	src := cs.Get(path)
	if src == nil {
		return "", fmt.Errorf("collection %q not found", path)
	}
	lp, lq := strings.ToLower(parent), strings.ToLower(path)
	if lp == lq || strings.HasPrefix(lp, lq+Sep) {
		return "", errors.New("cannot move a folder into itself")
	}
	dst, err := cs.siblings(parent)
	if err != nil {
		return "", err
	}
	if indexOf(*dst, src.Name) >= 0 {
		return "", fmt.Errorf("collection %q already exists there", src.Name)
	}
	moved := *src // copy before Delete shifts the slice
	if err := cs.Delete(path); err != nil {
		return "", err
	}
	dst, err = cs.siblings(parent) // Delete may have moved the backing arrays
	if err != nil {
		return "", err
	}
	*dst = append(*dst, moved)
	if parent == "" {
		return moved.Name, nil
	}
	return parent + Sep + moved.Name, nil
}

// TransferJobs moves (or, with keep, copies) Jobs from the folder at from into
// the folder at to. A Job already in the target is not added twice.
func (cs *Collections) TransferJobs(from, to string, jobs []string, keep bool) error {
	src, dst := cs.Get(from), cs.Get(to)
	if src == nil {
		return fmt.Errorf("collection %q not found", from)
	}
	if dst == nil {
		return fmt.Errorf("collection %q not found", to)
	}
	if src == dst {
		return errors.New("choose a different folder")
	}
	have := map[string]bool{}
	for _, j := range dst.Jobs {
		have[j] = true
	}
	for _, j := range jobs {
		if !have[j] {
			have[j] = true
			dst.Jobs = append(dst.Jobs, j)
		}
	}
	if !keep {
		drop := map[string]bool{}
		for _, j := range jobs {
			drop[j] = true
		}
		src.Jobs = slices.DeleteFunc(src.Jobs, func(j string) bool { return drop[j] })
	}
	return nil
}

// LoadCollections reads collections.toml; a missing file yields an empty set.
func LoadCollections(path string) (*Collections, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Collections{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cs Collections
	if err := toml.Unmarshal(b, &cs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cs, nil
}

// SaveCollections writes collections.toml atomically.
func SaveCollections(path string, cs *Collections) error {
	b, err := toml.Marshal(cs)
	if err != nil {
		return err
	}
	return writeFile(path, b)
}
