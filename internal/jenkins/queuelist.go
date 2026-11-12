package jenkins

import (
	"context"
	"net/url"
)

// QueuedJobs returns how many items wait in the queue for each job, by the
// job's full name. Items whose task has no full name (not a job) are left
// out.
func (c *Client) QueuedJobs(ctx context.Context) (map[string]int, error) {
	var r struct {
		Items []*struct {
			Task *struct {
				FullName string `json:"fullName"`
			} `json:"task"`
		} `json:"items"`
	}
	const tree = "items[task[fullName]]"
	if _, err := c.do(ctx, "GET", "/queue/api/json?tree="+url.QueryEscape(tree), &r); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, it := range r.Items {
		if it != nil && it.Task != nil && it.Task.FullName != "" {
			out[it.Task.FullName]++
		}
	}
	return out, nil
}
