package jenkins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ErrQueueItemNotFound is returned by CancelQueue when Jenkins does not know
// the item: it already started, was already cancelled, or never existed.
var ErrQueueItemNotFound = errors.New("queue item not found (it may have started or been cancelled already)")

// StopBuild asks Jenkins to abort a running build. Stopping a build that has
// already finished is not an error: Jenkins ignores it and answers as usual.
// A stale CSRF crumb is refreshed and retried once. ErrBuildNotFound means
// Jenkins has no such build.
func (c *Client) StopBuild(ctx context.Context, fullName string, number int) error {
	if strings.Trim(fullName, "/") == "" {
		return errors.New("stop: empty job name")
	}
	if number <= 0 {
		return fmt.Errorf("stop: invalid build number %d", number)
	}
	return c.postAction(ctx, jobPath(fullName)+"/"+strconv.Itoa(number)+"/stop", ErrBuildNotFound)
}

// CancelQueue removes a waiting item from the build queue. ErrQueueItemNotFound
// means Jenkins does not know the item (it started or was cancelled already).
func (c *Client) CancelQueue(ctx context.Context, id int) error {
	if id <= 0 {
		return fmt.Errorf("cancel: invalid queue id %d", id)
	}
	return c.postAction(ctx, "/queue/cancelItem?id="+strconv.Itoa(id), ErrQueueItemNotFound)
}

// postAction POSTs an empty-bodied control request with the CSRF crumb, as
// Trigger does: it proceeds without a crumb when the issuer refuses, and
// retries once with a refreshed crumb when Jenkins rejects it with a 403.
//
// Redirects are not followed. Jenkins answers stop and cancelItem with 302 to
// the build or queue page (newer versions 204); the action has already
// happened by then, so a redirect target that errors (a page the user may not
// view, a proxy login page) must not turn a successful stop into a failure.
// Any 2xx or 3xx is success and the body is never decoded.
func (c *Client) postAction(ctx context.Context, path string, notFound error) error {
	hc := *c.hc // shallow copy: same jar and transport, own redirect policy
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	refresh := false
	for attempt := 0; ; attempt++ {
		field, val, err := c.crumb(ctx, refresh)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, nil)
		if err != nil {
			return err
		}
		if c.user != "" {
			req.SetBasicAuth(c.user, c.tok)
		}
		if field != "" {
			req.Header.Set(field, val)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return classify(err)
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusForbidden && strings.Contains(strings.ToLower(string(b)), "crumb") && attempt == 0:
			refresh = true
			continue
		case resp.StatusCode == http.StatusUnauthorized:
			return ErrAuth
		case resp.StatusCode == http.StatusForbidden:
			what := "403 Forbidden"
			if strings.Contains(strings.ToLower(string(b)), "crumb") {
				what = "403: CSRF crumb rejected"
			}
			if d := serverDetail(b); d != "" {
				what += ": " + d
			}
			return fmt.Errorf("%w (%s)", ErrForbidden, what)
		case resp.StatusCode == http.StatusNotFound:
			return notFound
		case resp.StatusCode >= 400:
			return fmt.Errorf("jenkins returned %s for POST %s", resp.Status, strings.SplitN(path, "?", 2)[0])
		}
		return nil
	}
}
