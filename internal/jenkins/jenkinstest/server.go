// Package jenkinstest provides an in-process fake Jenkins server that mimics
// the real JSON API shapes, for use in tests across the repository.
package jenkinstest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Parameter definition types, as reported in "type" by Jenkins.
const (
	TypeString   = "StringParameterDefinition"
	TypeBoolean  = "BooleanParameterDefinition"
	TypeChoice   = "ChoiceParameterDefinition"
	TypePassword = "PasswordParameterDefinition"
	TypeText     = "TextParameterDefinition"
)

// Stage statuses as reported by the Pipeline REST API (wfapi).
const (
	StageSuccess     = "SUCCESS"
	StageFailed      = "FAILED"
	StageInProgress  = "IN_PROGRESS"
	StageNotExecuted = "NOT_EXECUTED"
	StagePausedInput = "PAUSED_PENDING_INPUT"
)

// CrumbHeaderName is the CSRF header the fake expects when the crumb issuer is on.
const (
	CrumbHeaderName   = "Jenkins-Crumb"
	defaultCrumbValue = "fake-crumb"
)

// Param is a parameter definition for a job.
type Param struct {
	Type        string
	Name        string
	Description string
	Default     string
	Choices     []string
}

// StringParam builds a StringParameterDefinition.
func StringParam(name, desc, def string) Param {
	return Param{Type: TypeString, Name: name, Description: desc, Default: def}
}

// BoolParam builds a BooleanParameterDefinition.
func BoolParam(name, desc string, def bool) Param {
	return Param{Type: TypeBoolean, Name: name, Description: desc, Default: strconv.FormatBool(def)}
}

// ChoiceParam builds a ChoiceParameterDefinition; the first choice is the default.
func ChoiceParam(name, desc string, choices ...string) Param {
	p := Param{Type: TypeChoice, Name: name, Description: desc, Choices: choices}
	if len(choices) > 0 {
		p.Default = choices[0]
	}
	return p
}

// PasswordParam builds a PasswordParameterDefinition.
func PasswordParam(name, desc string) Param {
	return Param{Type: TypePassword, Name: name, Description: desc}
}

// TextParam builds a TextParameterDefinition (multi-line string).
func TextParam(name, desc, def string) Param {
	return Param{Type: TypeText, Name: name, Description: desc, Default: def}
}

// Build describes a job's last build.
type Build struct {
	Number    int
	Result    string // SUCCESS, FAILURE, UNSTABLE, ABORTED; ignored while Building
	Building  bool
	Timestamp time.Time
	Duration  time.Duration
}

// Stage is a pipeline stage; Children model parallel branches.
type Stage struct {
	Name     string
	Status   string
	Duration time.Duration
	Children []Stage
}

// QueueItem is an entry in the build queue.
type QueueItem struct {
	ID         int
	Job        string // full name, e.g. "team/build"
	Why        string
	Params     url.Values
	Executable int // build number once the item has left the queue; 0 = still queued

	// Optional queue-state flags reported in the item JSON.
	Blocked, Buildable, Cancelled bool
}

// Trigger records one POST to /build or /buildWithParameters.
type Trigger struct {
	Job      string
	Endpoint string // "build" or "buildWithParameters"
	Params   url.Values
	QueueID  int
}

type node struct {
	name     string
	folder   bool
	multi    bool // multibranch project (also a folder)
	params   []Param
	last     *Build
	disabled bool
	pipeline bool
	stages   []Stage
}

// Server is a fake Jenkins.
type Server struct {
	// URL is the base URL of the server.
	URL string

	t  *testing.T
	ts *httptest.Server

	mu        sync.Mutex
	user, tok string
	crumbOn   bool
	crumb     string
	// crumbSession ties the crumb to a web session as Jenkins >= 2.176 does:
	// the issuer sets a JSESSIONID cookie and POSTs must send it back.
	crumbSession   bool
	crumbForbidden bool             // the crumb issuer itself answers 403
	nodes          map[string]*node // key: full name; "" is root
	queue          []QueueItem
	nextQueue      int
	triggers       []Trigger
}

// New starts a fake Jenkins that is closed via t.Cleanup. Default credentials
// are "me"/"tok"; the crumb issuer is off.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{
		t: t, user: "me", tok: "tok", crumb: defaultCrumbValue, nextQueue: 100,
		nodes: map[string]*node{"": {folder: true}},
	}
	s.ts = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.ts.URL
	t.Cleanup(s.ts.Close)
	return s
}

// Client returns the underlying HTTP client usable for TLS-less requests.
func (s *Server) Client() *http.Client { return s.ts.Client() }

// SetAuth sets the accepted basic-auth credentials.
func (s *Server) SetAuth(user, token string) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user, s.tok = user, token
	return s
}

// User and Token return the accepted credentials.
func (s *Server) User() string  { s.mu.Lock(); defer s.mu.Unlock(); return s.user }
func (s *Server) Token() string { s.mu.Lock(); defer s.mu.Unlock(); return s.tok }

// EnableCrumb toggles the CSRF crumb issuer. When on, POSTs must carry the
// crumb header (CrumbHeaderName) with value Crumb(), else 403.
func (s *Server) EnableCrumb(on bool) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crumbOn = on
	return s
}

// SessionBoundCrumb makes the crumb valid only together with the session
// cookie handed out by the crumb issuer (real Jenkins behaviour since 2.176).
// Requires EnableCrumb(true).
func (s *Server) SessionBoundCrumb(on bool) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crumbSession = on
	return s
}

// CrumbIssuerForbidden makes /crumbIssuer itself answer 403.
func (s *Server) CrumbIssuerForbidden(on bool) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crumbForbidden = on
	return s
}

// Crumb returns the crumb value issued while the issuer is enabled.
func (s *Server) Crumb() string { s.mu.Lock(); defer s.mu.Unlock(); return s.crumb }

// RotateCrumb changes the crumb value, making any previously issued crumb stale.
func (s *Server) RotateCrumb(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crumb = v
}

// RemoveQueueItem drops an item from the queue; its API then answers 404, as
// real Jenkins does once an item has left the queue.
func (s *Server) RemoveQueueItem(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.queue {
		if s.queue[i].ID == id {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return
		}
	}
}

// Folder defines a folder (and any missing parent folders) and returns a builder for it.
func (s *Server) Folder(path string) *Folder {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure(path, true)
	return &Folder{s: s, path: strings.Trim(path, "/")}
}

// Job defines a job at a full path (missing parent folders are created) and returns its builder.
func (s *Server) Job(path string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.ensure(path, false)
	return &Job{s: s, n: n, path: strings.Trim(path, "/")}
}

func (s *Server) ensure(path string, folder bool) *node {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := range parts {
		key := strings.Join(parts[:i+1], "/")
		last := i == len(parts)-1
		if n, ok := s.nodes[key]; ok {
			if last {
				return n
			}
			continue
		}
		s.nodes[key] = &node{name: parts[i], folder: !last || folder}
	}
	return s.nodes[strings.Join(parts, "/")]
}

// Folder is a builder for a folder.
type Folder struct {
	s    *Server
	path string
}

// Job defines a job inside the folder.
func (f *Folder) Job(name string) *Job { return f.s.Job(f.path + "/" + name) }

// Folder defines a sub-folder.
func (f *Folder) Folder(name string) *Folder { return f.s.Folder(f.path + "/" + name) }

// Job is a builder for a job.
type Job struct {
	s    *Server
	n    *node
	path string
}

// Params appends parameter definitions.
func (j *Job) Params(p ...Param) *Job {
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	j.n.params = append(j.n.params, p...)
	return j
}

// LastBuild sets the job's last build.
func (j *Job) LastBuild(b Build) *Job {
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	j.n.last = &b
	return j
}

// Disabled marks the job disabled (color "disabled").
func (j *Job) Disabled() *Job {
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	j.n.disabled = true
	return j
}

// Pipeline marks the job as a pipeline with the given stages for its last
// build; wfapi endpoints then answer instead of 404.
func (j *Job) Pipeline(stages ...Stage) *Job {
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	j.n.pipeline = true
	j.n.stages = stages
	return j
}

// AddQueueItem adds an item to the queue. ID 0 allocates one; the final ID is returned.
func (s *Server) AddQueueItem(q QueueItem) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addQueue(q)
}

func (s *Server) addQueue(q QueueItem) int {
	if q.ID == 0 {
		s.nextQueue++
		q.ID = s.nextQueue
	}
	s.queue = append(s.queue, q)
	return q.ID
}

// ResolveQueueItem marks a queue item as started as build number n.
func (s *Server) ResolveQueueItem(id, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.queue {
		if s.queue[i].ID == id {
			s.queue[i].Executable = n
		}
	}
}

// Triggers returns all recorded build triggers in order.
func (s *Server) Triggers() []Trigger {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Trigger(nil), s.triggers...)
}

// LastTrigger returns the most recent trigger, if any.
func (s *Server) LastTrigger() (Trigger, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.triggers) == 0 {
		return Trigger{}, false
	}
	return s.triggers[len(s.triggers)-1], true
}

// ---- HTTP ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if u, p, ok := r.BasicAuth(); !ok || u != s.user || p != s.tok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Jenkins"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	segs := splitPath(r.URL.EscapedPath())
	for i, sg := range segs {
		// Decode once, but keep an encoded '/' encoded: branch job names are
		// stored URL-encoded ("feature%2Fx") exactly as Jenkins names them.
		if d, err := url.PathUnescape(sg); err == nil {
			segs[i] = strings.ReplaceAll(d, "/", "%2F")
		}
	}

	if len(segs) == 3 && segs[0] == "crumbIssuer" {
		if s.crumbForbidden {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if !s.crumbOn {
			http.NotFound(w, r)
			return
		}
		if s.crumbSession {
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "fake-session", Path: "/"})
		}
		writeJSON(w, map[string]any{
			"_class": "hudson.security.csrf.DefaultCrumbIssuer", "crumb": s.crumb, "crumbRequestField": CrumbHeaderName,
		})
		return
	}
	if r.Method == http.MethodPost && s.crumbOn {
		bad := r.Header.Get(CrumbHeaderName) != s.crumb
		if c, err := r.Cookie("JSESSIONID"); s.crumbSession && (err != nil || c.Value != "fake-session") {
			bad = true
		}
		if bad {
			http.Error(w, "No valid crumb was included in the request", http.StatusForbidden)
			return
		}
	}

	switch {
	case len(segs) == 3 && segs[0] == "me" && segs[1] == "api":
		writeJSON(w, map[string]any{"_class": "hudson.model.User", "fullName": "Me Myself", "id": s.user})
		return
	case len(segs) >= 2 && segs[0] == "queue":
		s.serveQueue(w, r, segs)
		return
	}

	// /job/a/job/b/<rest...>
	var names []string
	i := 0
	for i+1 < len(segs) && segs[i] == "job" {
		names = append(names, segs[i+1])
		i += 2
	}
	rest := segs[i:]
	full := strings.Join(names, "/")
	n, ok := s.nodes[full]
	if !ok {
		// Tolerate equivalent encodings of a segment ("x y" vs "x%20y").
		for k, cand := range s.nodes {
			if canon(k) == canon(full) {
				n, full, ok = cand, k, true
				break
			}
		}
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case len(rest) == 2 && rest[0] == "api" && rest[1] == "json" && r.Method == http.MethodGet:
		writeJSON(w, s.itemJSON(full, n))
	case r.Method == http.MethodPost && len(rest) == 1 && (rest[0] == "build" || rest[0] == "buildWithParameters") && !n.folder:
		_ = r.ParseForm()
		id := s.addQueue(QueueItem{Job: full, Why: "Waiting for next available executor", Params: r.Form})
		s.triggers = append(s.triggers, Trigger{Job: full, Endpoint: rest[0], Params: r.Form, QueueID: id})
		w.Header().Set("Location", fmt.Sprintf("%s/queue/item/%d/", s.URL, id))
		w.WriteHeader(http.StatusCreated)
	case len(rest) == 3 && rest[1] == "api" && rest[2] == "json" && r.Method == http.MethodGet:
		if n.folder || n.last == nil || (rest[0] != "lastBuild" && rest[0] != strconv.Itoa(n.last.Number)) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, s.buildJSON(full, n.last))
	case len(rest) == 2 && rest[0] == "wfapi" && rest[1] == "runs":
		if !n.pipeline {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, s.runsJSON(n))
	case len(rest) == 3 && rest[1] == "wfapi" && rest[2] == "describe":
		if !n.pipeline || n.last == nil {
			http.NotFound(w, r)
			return
		}
		if rest[0] != "lastBuild" && rest[0] != strconv.Itoa(n.last.Number) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, s.runJSON(n))
	default:
		http.NotFound(w, r)
	}
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (s *Server) children(full string) []string {
	var out []string
	for k := range s.nodes {
		if k == "" || k == full {
			continue
		}
		parent := ""
		if i := strings.LastIndex(k, "/"); i >= 0 {
			parent = k[:i]
		}
		if parent == full {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) itemURL(full string) string {
	var sb strings.Builder
	sb.WriteString(s.URL)
	if full != "" {
		for _, p := range strings.Split(full, "/") {
			sb.WriteString("/job/" + escSeg(p))
		}
	}
	sb.WriteString("/")
	return sb.String()
}

func color(n *node) string {
	if n.disabled {
		return "disabled"
	}
	if n.last == nil {
		return "notbuilt"
	}
	base := "notbuilt"
	switch n.last.Result {
	case "SUCCESS":
		base = "blue"
	case "FAILURE":
		base = "red"
	case "UNSTABLE":
		base = "yellow"
	case "ABORTED":
		base = "aborted"
	}
	if n.last.Building {
		return base + "_anime"
	}
	return base
}

func (s *Server) itemJSON(full string, n *node) map[string]any {
	m := map[string]any{"name": n.name, "fullName": full, "url": s.itemURL(full)}
	if full == "" {
		m["_class"] = "hudson.model.Hudson"
		m["name"] = ""
	}
	if n.folder {
		if full != "" {
			m["_class"] = folderClass(n)
		}
		jobs := []map[string]any{}
		for _, c := range s.children(full) {
			cn := s.nodes[c]
			jobs = append(jobs, s.childJSON(c, cn))
		}
		m["jobs"] = jobs
		return m
	}
	m["_class"] = "hudson.model.FreeStyleProject"
	if n.pipeline {
		m["_class"] = "org.jenkinsci.plugins.workflow.job.WorkflowJob"
	}
	m["color"] = color(n)
	m["buildable"] = !n.disabled
	m["inQueue"] = false
	for _, q := range s.queue {
		if q.Job == full && q.Executable == 0 {
			m["inQueue"] = true
		}
	}
	props := []map[string]any{}
	if len(n.params) > 0 {
		defs := make([]map[string]any, 0, len(n.params))
		for _, p := range n.params {
			defs = append(defs, paramJSON(p))
		}
		props = append(props, map[string]any{
			"_class": "hudson.model.ParametersDefinitionProperty", "parameterDefinitions": defs,
		})
	}
	m["property"] = props
	if n.last != nil {
		m["lastBuild"] = s.buildJSON(full, n.last)
	} else {
		m["lastBuild"] = nil
	}
	return m
}

// childJSON is the shape of an item inside a parent's "jobs" array.
func (s *Server) childJSON(full string, n *node) map[string]any {
	m := map[string]any{"name": n.name, "url": s.itemURL(full)}
	if n.folder {
		m["_class"] = folderClass(n)
		kids := []map[string]any{}
		for _, c := range s.children(full) {
			kids = append(kids, map[string]any{"name": s.nodes[c].name})
		}
		m["jobs"] = kids
		return m
	}
	m["_class"] = "hudson.model.FreeStyleProject"
	if n.pipeline {
		m["_class"] = "org.jenkinsci.plugins.workflow.job.WorkflowJob"
	}
	m["color"] = color(n)
	return m
}

func paramJSON(p Param) map[string]any {
	class := "hudson.model." + p.Type
	m := map[string]any{"_class": class, "name": p.Name, "type": p.Type, "description": p.Description}
	var dv any = p.Default
	if p.Type == TypeBoolean {
		dv = p.Default == "true"
	}
	if p.Type == TypePassword {
		dv = "" // Jenkins never reveals password defaults
	}
	m["defaultParameterValue"] = map[string]any{"_class": class[:len(class)-len("Definition")] + "Value", "name": p.Name, "value": dv}
	if p.Type == TypeChoice {
		m["choices"] = append([]string{}, p.Choices...)
	}
	return m
}

func (s *Server) buildJSON(full string, b *Build) map[string]any {
	m := map[string]any{
		"_class": "hudson.model.FreeStyleBuild", "number": b.Number, "building": b.Building,
		"timestamp": b.Timestamp.UnixMilli(), "duration": b.Duration.Milliseconds(),
		"url": fmt.Sprintf("%s%d/", s.itemURL(full), b.Number),
	}
	if b.Building || b.Result == "" {
		m["result"] = nil
	} else {
		m["result"] = b.Result
	}
	return m
}

func (s *Server) serveQueue(w http.ResponseWriter, r *http.Request, segs []string) {
	toJSON := func(q QueueItem) map[string]any {
		m := map[string]any{
			"_class": "hudson.model.Queue$WaitingItem", "id": q.ID, "why": q.Why, "inQueueSince": time.Now().UnixMilli(),
			"task": map[string]any{"name": lastSeg(q.Job), "fullName": q.Job, "url": s.itemURL(q.Job)},
		}
		if q.Blocked {
			m["blocked"] = true
		}
		if q.Buildable {
			m["buildable"] = true
		}
		if q.Cancelled {
			m["cancelled"] = true
		}
		if q.Executable > 0 {
			m["executable"] = map[string]any{"number": q.Executable, "url": fmt.Sprintf("%s%d/", s.itemURL(q.Job), q.Executable)}
		}
		return m
	}
	switch {
	case len(segs) == 3 && segs[1] == "api" && segs[2] == "json":
		items := []map[string]any{}
		for _, q := range s.queue {
			if q.Executable == 0 {
				items = append(items, toJSON(q))
			}
		}
		writeJSON(w, map[string]any{"_class": "hudson.model.Queue", "items": items})
	case len(segs) == 5 && segs[1] == "item" && segs[3] == "api" && segs[4] == "json":
		id, _ := strconv.Atoi(segs[2])
		for _, q := range s.queue {
			if q.ID == id {
				writeJSON(w, toJSON(q))
				return
			}
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

func lastSeg(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func stageJSON(id int, st Stage) map[string]any {
	m := map[string]any{
		"id": strconv.Itoa(id), "name": st.Name, "status": st.Status,
		"startTimeMillis": 0, "durationMillis": st.Duration.Milliseconds(),
	}
	if len(st.Children) > 0 {
		var kids []map[string]any
		for i, c := range st.Children {
			kids = append(kids, stageJSON(id*100+i+1, c))
		}
		m["stageFlowNodes"] = kids
	}
	return m
}

func (s *Server) runJSON(n *node) map[string]any {
	status := "SUCCESS"
	switch {
	case n.last.Building:
		status = StageInProgress
	case n.last.Result == "FAILURE":
		status = "FAILED"
	case n.last.Result == "ABORTED":
		status = "ABORTED"
	case n.last.Result == "UNSTABLE":
		status = "UNSTABLE"
	}
	stages := []map[string]any{}
	for i, st := range n.stages {
		stages = append(stages, stageJSON(i+1, st))
	}
	return map[string]any{
		"id": strconv.Itoa(n.last.Number), "name": "#" + strconv.Itoa(n.last.Number), "status": status,
		"startTimeMillis": n.last.Timestamp.UnixMilli(), "durationMillis": n.last.Duration.Milliseconds(),
		"stages": stages,
	}
}

func (s *Server) runsJSON(n *node) []map[string]any {
	if n.last == nil {
		return []map[string]any{}
	}
	return []map[string]any{s.runJSON(n)}
}

const multibranchClass = "org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject"

func folderClass(n *node) string {
	if n.multi {
		return multibranchClass
	}
	return "com.cloudbees.hudson.plugins.folder.Folder"
}

// escSeg mirrors Jenkins URLs: names holding '%' are already-encoded branch names.
func escSeg(p string) string {
	if strings.Contains(p, "%") {
		return p
	}
	return url.PathEscape(p)
}

// Multibranch defines a Multibranch Project (and any missing parent folders).
// It has no branches until Branch is called.
func (s *Server) Multibranch(path string) *Multibranch {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.ensure(path, true)
	n.multi = true
	return &Multibranch{s: s, path: strings.Trim(path, "/")}
}

// Multibranch is a builder for a Multibranch Project.
type Multibranch struct {
	s    *Server
	path string
}

// Branch defines a branch job. The decoded name may contain '/' ("feature/x");
// like Jenkins, the job's name and URL segment are the URL-encoded form
// ("feature%2Fx"), so the returned Job's path uses the encoded name.
func (m *Multibranch) Branch(name string) *Job {
	return m.s.Job(m.path + "/" + url.PathEscape(name)).Pipeline()
}

// canon normalises every segment of a node key to one encoding for comparison.
func canon(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		if d, err := url.PathUnescape(p); err == nil {
			p = d
		}
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
