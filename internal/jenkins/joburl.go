package jenkins

import "strconv"

// JobURL is the browser URL of a job, built from the configured base URL (not
// from the url Jenkins reports, which can be internal or wrong behind a
// proxy), with a trailing slash: https://ci.example/job/a/job/b/.
func (c *Client) JobURL(fullName string) string { return c.base + jobPath(fullName) + "/" }

// BuildURL is the browser URL of build num of a job, built like JobURL.
func (c *Client) BuildURL(fullName string, num int) string {
	return c.base + jobPath(fullName) + "/" + strconv.Itoa(num) + "/"
}
