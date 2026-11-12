package jenkins

import (
	"context"
	"net/http"
	"net/url"
)

// FolderColors returns the colour hint ("red", "blue_anime", ...) of every
// item directly under folder ("" = the Jenkins root), keyed by the item's
// name as Jenkins reports it (a Multibranch branch stays URL-encoded, as in
// a job's full name). One request covers the whole folder, which is cheaper
// than reading each job's last build.
func (c *Client) FolderColors(ctx context.Context, folder string) (map[string]string, error) {
	var r struct {
		Jobs []*struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"jobs"`
	}
	const tree = "jobs[name,color]"
	if _, err := c.do(ctx, http.MethodGet, jobPath(folder)+"/api/json?tree="+url.QueryEscape(tree), &r); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(r.Jobs))
	for _, j := range r.Jobs {
		if j != nil {
			out[j.Name] = j.Color
		}
	}
	return out, nil
}
