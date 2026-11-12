package jenkins

// JobURL is the browser URL of a job, e.g. https://ci.example/job/a/job/b.
func (c *Client) JobURL(fullName string) string { return c.base + jobPath(fullName) }
