package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Collection is a named, ordered group of Jobs (by full path).
type Collection struct {
	Name string   `toml:"name"`
	Jobs []string `toml:"jobs"`
}

// Collections is the persisted set of Collections, in display order.
type Collections struct {
	Items []Collection `toml:"collection"`
}

// Find returns the index of the named Collection, or -1.
func (cs *Collections) Find(name string) int {
	for i, c := range cs.Items {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}

// Add appends a new Collection. Names must be non-empty and unique (case-insensitive).
func (cs *Collections) Add(name string, jobs []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("collection name is required")
	}
	if cs.Find(name) >= 0 {
		return fmt.Errorf("collection %q already exists", name)
	}
	cs.Items = append(cs.Items, Collection{Name: name, Jobs: append([]string(nil), jobs...)})
	return nil
}

// Rename changes a Collection's name, keeping names unique.
func (cs *Collections) Rename(old, name string) error {
	i := cs.Find(old)
	if i < 0 {
		return fmt.Errorf("collection %q not found", old)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("collection name is required")
	}
	if j := cs.Find(name); j >= 0 && j != i {
		return fmt.Errorf("collection %q already exists", name)
	}
	cs.Items[i].Name = name
	return nil
}

// SetJobs replaces a Collection's Jobs (order preserved, duplicates dropped).
func (cs *Collections) SetJobs(name string, jobs []string) error {
	i := cs.Find(name)
	if i < 0 {
		return fmt.Errorf("collection %q not found", name)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		if !seen[j] {
			seen[j] = true
			out = append(out, j)
		}
	}
	cs.Items[i].Jobs = out
	return nil
}

// Delete removes a Collection.
func (cs *Collections) Delete(name string) error {
	i := cs.Find(name)
	if i < 0 {
		return fmt.Errorf("collection %q not found", name)
	}
	cs.Items = append(cs.Items[:i], cs.Items[i+1:]...)
	return nil
}

// MoveJob moves the Job at index from to index to within a Collection (reorder).
func (cs *Collections) MoveJob(name string, from, to int) error {
	i := cs.Find(name)
	if i < 0 {
		return fmt.Errorf("collection %q not found", name)
	}
	jobs := cs.Items[i].Jobs
	if from < 0 || from >= len(jobs) || to < 0 || to >= len(jobs) {
		return errors.New("index out of range")
	}
	j := jobs[from]
	jobs = append(jobs[:from], jobs[from+1:]...)
	jobs = append(jobs[:to], append([]string{j}, jobs[to:]...)...)
	cs.Items[i].Jobs = jobs
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
