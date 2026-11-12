// Package jenkins is a small read/trigger client for the Jenkins JSON API.
package jenkins

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Classified failure kinds, for readable UI errors.
var (
	ErrAuth = errors.New("authentication failed (check username and password or API token)")
	// ErrForbidden means Jenkins understood the credentials but refused the
	// action (HTTP 403): missing permission or a rejected CSRF crumb. The
	// wrapped message carries Jenkins' own explanation.
	ErrForbidden = errors.New("Jenkins refused the request")
	ErrTLS       = errors.New("TLS verification failed")
	ErrNetwork   = errors.New("cannot reach Jenkins")
)

// Options configures a Client.
type Options struct {
	BaseURL            string
	Username, Token    string // held in memory only
	CACertPath         string
	InsecureSkipVerify bool
	HTTPClient         *http.Client // optional, for tests
}

// Client talks to one Jenkins server.
type Client struct {
	base string
	user string
	tok  string
	hc   *http.Client

	crumbMu    sync.Mutex
	crumbReady bool   // crumb state has been resolved (possibly "disabled")
	crumbField string // empty when the crumb issuer is disabled
	crumbValue string
}

// New builds a Client with the requested TLS settings.
func New(o Options) (*Client, error) {
	hc := o.HTTPClient
	if hc == nil {
		tc := &tls.Config{InsecureSkipVerify: o.InsecureSkipVerify} //nolint:gosec // opt-in via config
		if o.CACertPath != "" {
			pem, err := os.ReadFile(o.CACertPath)
			if err != nil {
				return nil, fmt.Errorf("read ca_cert: %w", err)
			}
			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("ca_cert contains no valid PEM certificates")
			}
			tc.RootCAs = pool
		}
		// Jenkins (>= 2.176) ties a CSRF crumb to the web session, so the session
		// cookie from /crumbIssuer must accompany the POST that uses the crumb.
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		hc = &http.Client{Timeout: 30 * time.Second, Jar: jar, Transport: &http.Transport{TLSClientConfig: tc, Proxy: http.ProxyFromEnvironment}}
	}
	return &Client{base: strings.TrimRight(o.BaseURL, "/"), user: o.Username, tok: o.Token, hc: hc}, nil
}

// jobPath converts "a/b/c" into "job/a/job/b/job/c".
func jobPath(fullName string) string {
	if fullName == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(fullName, "/"), "/")
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString("/job/")
		sb.WriteString(escSeg(p))
	}
	return sb.String()
}

// escSeg escapes one job-name path segment. Jenkins forbids '%' in job names,
// so a segment containing '%' is a multibranch branch name that Jenkins has
// already URL-encoded (e.g. "feature%2Fx") and is used verbatim.
func escSeg(p string) string {
	if strings.Contains(p, "%") {
		return p
	}
	return url.PathEscape(p)
}

func (c *Client) do(ctx context.Context, method, path string, out any) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.tok)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, classify(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return resp, ErrAuth
	case resp.StatusCode >= 400:
		return resp, fmt.Errorf("jenkins returned %s for %s %s", resp.Status, method, path)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp, fmt.Errorf("decode response: %w", err)
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return resp, nil
}

func classify(err error) error {
	var ua x509.UnknownAuthorityError
	var hn x509.HostnameError
	var ci x509.CertificateInvalidError
	if errors.As(err, &ua) || errors.As(err, &hn) || errors.As(err, &ci) || strings.Contains(err.Error(), "tls:") {
		return fmt.Errorf("%w: %v", ErrTLS, err)
	}
	return fmt.Errorf("%w: %v", ErrNetwork, err)
}

// Ping verifies reachability and credentials. Returns the authenticated user's full name.
func (c *Client) Ping(ctx context.Context) (string, error) {
	var me struct {
		FullName string `json:"fullName"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/me/api/json?tree=fullName", &me); err != nil {
		return "", err
	}
	return me.FullName, nil
}

// Job is a buildable item with its full path.
type Job struct {
	FullName string // path with Jenkins' own (URL-encoded) branch names, usable with the client
	Color    string // Jenkins "color" status hint, e.g. "blue", "red", "blue_anime"
	URL      string // the job's own web URL as Jenkins reports it; empty when unknown

	// Multibranch is the full path of the owning Multibranch Project; empty
	// for ordinary jobs. Branch is the decoded branch name ("feature/x").
	Multibranch string
	Branch      string
}

// Catalog is everything ListCatalog finds under a parent.
type Catalog struct {
	Jobs []Job
	// MultibranchProjects lists the full path of every Multibranch Project,
	// including those with no branches, sorted.
	MultibranchProjects []string
}

type jobNode struct {
	Class string     `json:"_class"`
	Name  string     `json:"name"`
	Color string     `json:"color"`
	URL   string     `json:"url"`
	Jobs  *[]jobNode `json:"jobs"` // non-nil for folder-like items
}

func (n jobNode) multibranch() bool { return strings.Contains(n.Class, "WorkflowMultiBranchProject") }

// ListJobs recursively lists every buildable Job under parent ("" = root), sorted by path.
// Folder-like items (folders, multibranch projects) are expanded, not returned;
// branches of Multibranch Projects are returned as Jobs with Multibranch set.
func (c *Client) ListJobs(ctx context.Context, parent string) ([]Job, error) {
	cat, err := c.ListCatalog(ctx, parent)
	return cat.Jobs, err
}

// ListCatalog is ListJobs plus the Multibranch Projects found on the way.
func (c *Client) ListCatalog(ctx context.Context, parent string) (Catalog, error) {
	var cat Catalog
	var walk func(folder, project string) error
	walk = func(folder, project string) error {
		var resp struct {
			Class string    `json:"_class"`
			Jobs  []jobNode `json:"jobs"`
		}
		if _, err := c.do(ctx, http.MethodGet, jobPath(folder)+"/api/json?tree=_class,jobs[_class,name,url,color,jobs[name]]", &resp); err != nil {
			return err
		}
		// The parent itself may be a Multibranch Project: its children are
		// branches, not plain jobs.
		if folder != "" && folder == strings.Trim(parent, "/") && project == "" && (jobNode{Class: resp.Class}).multibranch() {
			project = folder
			cat.MultibranchProjects = append(cat.MultibranchProjects, folder)
		}
		for _, n := range resp.Jobs {
			full := n.Name
			if folder != "" {
				full = folder + "/" + n.Name
			}
			switch {
			case project == "" && n.multibranch():
				cat.MultibranchProjects = append(cat.MultibranchProjects, full)
				if err := walk(full, full); err != nil {
					return err
				}
			case n.Jobs != nil:
				if err := walk(full, ""); err != nil {
					return err
				}
			case project != "":
				branch, err := url.PathUnescape(n.Name)
				if err != nil {
					branch = n.Name
				}
				cat.Jobs = append(cat.Jobs, Job{FullName: full, Color: n.Color, URL: n.URL, Multibranch: project, Branch: branch})
			default:
				cat.Jobs = append(cat.Jobs, Job{FullName: full, Color: n.Color, URL: n.URL})
			}
		}
		return nil
	}
	root := strings.Trim(parent, "/")
	if err := walk(root, ""); err != nil {
		return Catalog{}, err
	}
	sort.Slice(cat.Jobs, func(i, j int) bool { return cat.Jobs[i].FullName < cat.Jobs[j].FullName })
	sort.Strings(cat.MultibranchProjects)
	return cat, nil
}

// ParamDef describes one build parameter of a job.
type ParamDef struct {
	Name        string
	Type        string // normalised: string, bool, choice, password, text, other
	Description string
	Default     string
	Choices     []string
}

func normaliseParamType(typ, class string) string {
	t := typ
	if t == "" {
		t = class
	}
	if i := strings.LastIndexAny(t, ".$"); i >= 0 {
		t = t[i+1:]
	}
	switch t {
	case "StringParameterDefinition":
		return "string"
	case "BooleanParameterDefinition":
		return "bool"
	case "ChoiceParameterDefinition":
		return "choice"
	case "PasswordParameterDefinition":
		return "password"
	case "TextParameterDefinition":
		return "text"
	}
	return "other"
}

// JobParameters returns the parameter definitions of a job (empty, not an
// error, when the job takes none).
func (c *Client) JobParameters(ctx context.Context, fullName string) ([]ParamDef, error) {
	const tree = "property[parameterDefinitions[name,type,description,defaultParameterValue[value],choices]]"
	var resp struct {
		Property []struct {
			ParameterDefinitions []struct {
				Class        string `json:"_class"`
				Name         string `json:"name"`
				Type         string `json:"type"`
				Description  string `json:"description"`
				DefaultValue *struct {
					Value any `json:"value"`
				} `json:"defaultParameterValue"`
				Choices []string `json:"choices"`
			} `json:"parameterDefinitions"`
		} `json:"property"`
	}
	if _, err := c.do(ctx, http.MethodGet, jobPath(fullName)+"/api/json?tree="+url.QueryEscape(tree), &resp); err != nil {
		return nil, err
	}
	out := []ParamDef{}
	for _, p := range resp.Property {
		for _, d := range p.ParameterDefinitions {
			pd := ParamDef{Name: d.Name, Type: normaliseParamType(d.Type, d.Class), Description: d.Description, Choices: d.Choices}
			if d.DefaultValue != nil && d.DefaultValue.Value != nil {
				pd.Default = fmt.Sprint(d.DefaultValue.Value)
			}
			out = append(out, pd)
		}
	}
	return out, nil
}
