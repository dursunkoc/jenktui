package jenkins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ErrQueueItemGone is returned by ResolveQueue when Jenkins no longer knows the
// queue item (real Jenkins answers 404 shortly after an item leaves the queue).
var ErrQueueItemGone = errors.New("queue item no longer exists")

// QueueRef identifies a queued build.
type QueueRef struct {
	ID  int
	URL string
}

// QueueState is the state of a queue item.
type QueueState string

// Queue states.
const (
	QueueWaiting   QueueState = "waiting"
	QueueBlocked   QueueState = "blocked"
	QueueBuildable QueueState = "buildable"
	QueueCancelled QueueState = "cancelled"
	QueueStarted   QueueState = "started"
)

// QueueStatus is the resolved state of a queue item.
type QueueStatus struct {
	State       QueueState
	Why         string
	BuildNumber int // set when State == QueueStarted
}

// crumb returns the CSRF header name and value; name is "" when crumbs are
// disabled. The result is cached; refresh forces a refetch.
func (c *Client) crumb(ctx context.Context, refresh bool) (string, string, error) {
	c.crumbMu.Lock()
	defer c.crumbMu.Unlock()
	if c.crumbReady && !refresh {
		return c.crumbField, c.crumbValue, nil
	}
	var resp struct {
		Crumb string `json:"crumb"`
		Field string `json:"crumbRequestField"`
	}
	r, err := c.do(ctx, http.MethodGet, "/crumbIssuer/api/json?tree=crumb,crumbRequestField", &resp)
	switch {
	case err != nil && r != nil:
		// Jenkins answered but gave no crumb: 404 means crumbs are off, and
		// a 401/403 (or anything else) must not block the trigger because
		// API-token requests often need no crumb. Let the POST itself decide.
		c.crumbReady, c.crumbField, c.crumbValue = true, "", ""
		return "", "", nil
	case err != nil:
		return "", "", err // network or TLS failure
	}
	c.crumbReady, c.crumbField, c.crumbValue = true, resp.Field, resp.Crumb
	return c.crumbField, c.crumbValue, nil
}

// Trigger starts a build. With params it uses buildWithParameters (form
// encoded), otherwise build. A stale CSRF crumb is refreshed and retried once.
func (c *Client) Trigger(ctx context.Context, fullName string, params map[string]string) (QueueRef, error) {
	if strings.Trim(fullName, "/") == "" {
		return QueueRef{}, errors.New("trigger: empty job name")
	}
	endpoint, body := "/build", ""
	if len(params) > 0 {
		endpoint = "/buildWithParameters"
		form := url.Values{}
		for k, v := range params {
			form.Set(k, v)
		}
		body = form.Encode()
	}
	path := jobPath(fullName) + endpoint
	ct := ""
	if body != "" {
		ct = "application/x-www-form-urlencoded"
	}
	resp, _, err := c.postWithCrumb(ctx, c.hc, path, strings.NewReader(body), ct)
	if err != nil {
		return QueueRef{}, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return QueueRef{}, fmt.Errorf("%w: %s", ErrJobNotFound, fullName)
	case resp.StatusCode >= 400:
		return QueueRef{}, fmt.Errorf("jenkins returned %s for POST %s", resp.Status, path)
	}
	return parseQueueLocation(resp.Header.Get("Location"))
}

// postWithCrumb POSTs body to path with Basic auth and the CSRF crumb (it
// proceeds without a crumb when the issuer refuses), and retries once with a
// refreshed crumb when Jenkins rejects it with a 403 about the crumb. hc
// decides the redirect policy. It maps the answers every caller treats the
// same: 401 is ErrAuth, any other 403 is ErrForbidden with Jenkins' own text,
// and a network failure is classified. Every other response, 404 and other
// 4xx/5xx and redirects included, is returned (body already read and closed)
// for the caller to interpret. The body reader must support being read once
// per attempt: strings.Reader is rewound, so pass one (or nil).
func (c *Client) postWithCrumb(ctx context.Context, hc *http.Client, path string, body *strings.Reader, contentType string) (*http.Response, []byte, error) {
	refresh := false
	for attempt := 0; ; attempt++ {
		field, val, err := c.crumb(ctx, refresh)
		if err != nil {
			return nil, nil, err
		}
		var rd io.Reader
		if body != nil {
			body.Seek(0, io.SeekStart)
			rd = body
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, rd)
		if err != nil {
			return nil, nil, err
		}
		if c.user != "" {
			req.SetBasicAuth(c.user, c.tok)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if field != "" {
			req.Header.Set(field, val)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, nil, classify(err)
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusForbidden && strings.Contains(strings.ToLower(string(b)), "crumb") && attempt == 0:
			refresh = true
			continue
		case resp.StatusCode == http.StatusUnauthorized:
			return nil, nil, ErrAuth
		case resp.StatusCode == http.StatusForbidden:
			what := "403 Forbidden"
			if strings.Contains(strings.ToLower(string(b)), "crumb") {
				what = "403: CSRF crumb rejected"
			}
			if d := serverDetail(b); d != "" {
				what += ": " + d
			}
			return nil, nil, fmt.Errorf("%w (%s)", ErrForbidden, what)
		}
		return resp, b, nil
	}
}

func parseQueueLocation(loc string) (QueueRef, error) {
	u, err := url.Parse(loc)
	if loc == "" || err != nil {
		return QueueRef{}, errors.New("jenkins did not return a queue Location")
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) >= 3 && segs[len(segs)-3] == "queue" && segs[len(segs)-2] == "item" {
		if id, err := strconv.Atoi(segs[len(segs)-1]); err == nil {
			return QueueRef{ID: id, URL: loc}, nil
		}
	}
	return QueueRef{}, fmt.Errorf("unexpected queue Location %q", loc)
}

// ResolveQueue reports the state of a queued build. Returns ErrQueueItemGone
// when Jenkins has forgotten the item.
func (c *Client) ResolveQueue(ctx context.Context, ref QueueRef) (QueueStatus, error) {
	id := ref.ID
	if id == 0 && ref.URL != "" {
		parsed, err := parseQueueLocation(ref.URL)
		if err != nil {
			return QueueStatus{}, err
		}
		id = parsed.ID
	}
	if id == 0 {
		return QueueStatus{}, errors.New("resolve queue: empty reference")
	}
	var item struct {
		Why        string `json:"why"`
		Blocked    bool   `json:"blocked"`
		Buildable  bool   `json:"buildable"`
		Cancelled  bool   `json:"cancelled"`
		Executable *struct {
			Number int `json:"number"`
		} `json:"executable"`
	}
	const tree = "why,blocked,buildable,cancelled,executable[number]"
	resp, err := c.do(ctx, http.MethodGet, "/queue/item/"+strconv.Itoa(id)+"/api/json?tree="+url.QueryEscape(tree), &item)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return QueueStatus{}, ErrQueueItemGone
		}
		return QueueStatus{}, err
	}
	st := QueueStatus{Why: item.Why, State: QueueWaiting}
	switch {
	case item.Executable != nil && item.Executable.Number > 0:
		st.State, st.BuildNumber = QueueStarted, item.Executable.Number
	case item.Cancelled:
		st.State = QueueCancelled
	case item.Blocked:
		st.State = QueueBlocked
	case item.Buildable:
		st.State = QueueBuildable
	}
	return st, nil
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// serverDetail turns a Jenkins error body (often HTML) into one short line of
// plain text for display. It never includes request data, only the response.
func serverDetail(body []byte) string {
	s := htmlTag.ReplaceAllString(string(body), " ")
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}
