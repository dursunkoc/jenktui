package jenkins

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// LogChunk is a piece of a build's console log. Next is the offset to ask for
// next time; More reports that Jenkins expects further output (the build is
// still running).
type LogChunk struct {
	Text string
	Next int64
	More bool
}

// ConsoleText returns the console output of a build (number 0 = last build)
// from byte offset start, using Jenkins' progressive text endpoint. A missing
// build is ErrBuildNotFound.
func (c *Client) ConsoleText(ctx context.Context, fullName string, number int, start int64) (LogChunk, error) {
	path := fmt.Sprintf("%s/%s/logText/progressiveText?start=%d", jobPath(fullName), buildRef(number), start)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return LogChunk{}, err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.tok)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return LogChunk{}, classify(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return LogChunk{}, ErrBuildNotFound
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return LogChunk{}, ErrAuth
	case resp.StatusCode >= 400:
		return LogChunk{}, fmt.Errorf("jenkins returned %s for GET %s", resp.Status, path)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return LogChunk{}, fmt.Errorf("read console log: %w", err)
	}
	next := start + int64(len(body))
	if n, err := strconv.ParseInt(resp.Header.Get("X-Text-Size"), 10, 64); err == nil && n >= start {
		next = n
	}
	return LogChunk{Text: string(body), Next: next, More: resp.Header.Get("X-More-Data") == "true"}, nil
}
