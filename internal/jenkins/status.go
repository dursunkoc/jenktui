package jenkins

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors for status lookups.
var (
	ErrNoStages      = errors.New("no pipeline stages available")
	ErrBuildNotFound = errors.New("build not found")
)

// Cause is why a build started.
type Cause struct {
	Text string // Jenkins' short description, e.g. "Started by user Ada"
	User string // user name, falling back to the user id; "" for timers, upstream jobs
}

// Change is one commit in a build's change set.
type Change struct {
	ID      string
	Author  string
	Message string
}

// BuildParam is a parameter value a build ran with. Booleans are "true" or
// "false". Jenkins never reveals a password parameter: it has no value, so
// Password is true and Value is empty.
type BuildParam struct {
	Name     string
	Value    string
	Password bool
}

// Build is the state of one build.
type Build struct {
	Number            int
	Result            string // SUCCESS, FAILURE, UNSTABLE, ABORTED; "" while running
	Building          bool
	Timestamp         time.Time
	Duration          time.Duration
	EstimatedDuration time.Duration // 0 when Jenkins does not know
	URL               string
	Causes            []Cause
	Changes           []Change
	Params            []BuildParam // in the order Jenkins lists them
}

func buildRef(number int) string {
	if number <= 0 {
		return "lastBuild"
	}
	return strconv.Itoa(number)
}

// changeSetTree names every field read from a change set; real Jenkins serves
// "changeSets" for pipeline builds and "changeSet" for freestyle ones.
const changeSetTree = "items[commitId,author[fullName],msg]"

// buildStatusTree names every field (and sub-field) BuildStatus decodes.
const buildStatusTree = "number,result,building,timestamp,duration,estimatedDuration,url," +
	"actions[causes[shortDescription,userId,userName],parameters[name,value]]," +
	"changeSets[" + changeSetTree + "],changeSet[" + changeSetTree + "]"

type jsonChangeSet struct {
	Items []struct {
		CommitID string `json:"commitId"`
		Author   *struct {
			FullName string `json:"fullName"`
		} `json:"author"`
		Msg string `json:"msg"`
	} `json:"items"`
}

// BuildStatus returns a build of a job; number 0 means the last build.
func (c *Client) BuildStatus(ctx context.Context, fullName string, number int) (Build, error) {
	var r struct {
		Number            int     `json:"number"`
		Result            *string `json:"result"`
		Building          bool    `json:"building"`
		Timestamp         int64   `json:"timestamp"`
		Duration          int64   `json:"duration"`
		EstimatedDuration int64   `json:"estimatedDuration"`
		URL               string  `json:"url"`
		// actions mixes entries and holds nulls and {}: every field optional.
		Actions []*struct {
			Causes []*struct {
				ShortDescription string `json:"shortDescription"`
				UserID           string `json:"userId"`
				UserName         string `json:"userName"`
			} `json:"causes"`
			Parameters []*struct {
				Name  string `json:"name"`
				Value any    `json:"value"`
			} `json:"parameters"`
		} `json:"actions"`
		ChangeSets []*jsonChangeSet `json:"changeSets"`
		ChangeSet  *jsonChangeSet   `json:"changeSet"`
	}
	resp, err := c.do(ctx, http.MethodGet, jobPath(fullName)+"/"+buildRef(number)+"/api/json?tree="+url.QueryEscape(buildStatusTree), &r)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return Build{}, ErrBuildNotFound
		}
		return Build{}, err
	}
	b := Build{Number: r.Number, Building: r.Building, Duration: time.Duration(r.Duration) * time.Millisecond, URL: r.URL}
	if r.Result != nil && !r.Building {
		b.Result = *r.Result
	}
	if r.Timestamp > 0 {
		b.Timestamp = time.UnixMilli(r.Timestamp)
	}
	if r.EstimatedDuration > 0 {
		b.EstimatedDuration = time.Duration(r.EstimatedDuration) * time.Millisecond
	}
	for _, a := range r.Actions {
		if a == nil {
			continue
		}
		for _, cs := range a.Causes {
			if cs == nil {
				continue
			}
			user := cs.UserName
			if user == "" {
				user = cs.UserID
			}
			b.Causes = append(b.Causes, Cause{Text: cs.ShortDescription, User: user})
		}
		for _, p := range a.Parameters {
			if p == nil {
				continue
			}
			bp := BuildParam{Name: p.Name}
			switch v := p.Value.(type) {
			case nil:
				bp.Password = true
			case string:
				bp.Value = v
			case bool:
				bp.Value = strconv.FormatBool(v)
			case float64:
				bp.Value = strconv.FormatFloat(v, 'f', -1, 64)
			default:
				bp.Value = fmt.Sprint(v)
			}
			b.Params = append(b.Params, bp)
		}
	}
	sets := r.ChangeSets
	sets = append(sets, r.ChangeSet)
	seen := map[Change]bool{}
	for _, set := range sets {
		if set == nil {
			continue
		}
		for _, it := range set.Items {
			ch := Change{ID: it.CommitID, Message: it.Msg}
			if it.Author != nil {
				ch.Author = it.Author.FullName
			}
			if !seen[ch] {
				seen[ch] = true
				b.Changes = append(b.Changes, ch)
			}
		}
	}
	return b, nil
}

// ErrJobNotFound is returned when a job does not exist.
var ErrJobNotFound = errors.New("job not found")

// BuildHistory returns the last n builds of a job, newest first, with only
// Number, Result and Building set. One request.
func (c *Client) BuildHistory(ctx context.Context, fullName string, n int) ([]Build, error) {
	if n <= 0 {
		return nil, nil
	}
	tree := "builds[number,result,building]{0," + strconv.Itoa(n) + "}"
	var r struct {
		Builds []struct {
			Number   int     `json:"number"`
			Result   *string `json:"result"`
			Building bool    `json:"building"`
		} `json:"builds"`
	}
	resp, err := c.do(ctx, http.MethodGet, jobPath(fullName)+"/api/json?tree="+url.QueryEscape(tree), &r)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s", ErrJobNotFound, fullName)
		}
		return nil, err
	}
	out := make([]Build, 0, len(r.Builds))
	for _, b := range r.Builds {
		nb := Build{Number: b.Number, Building: b.Building}
		if b.Result != nil && !b.Building {
			nb.Result = *b.Result
		}
		out = append(out, nb)
	}
	return out, nil
}

// StageStatus is a normalised pipeline stage status.
type StageStatus string

// Stage statuses.
const (
	StageSuccess     StageStatus = "success"
	StageFailed      StageStatus = "failed"
	StageRunning     StageStatus = "running"
	StageNotExecuted StageStatus = "not-executed" // queued or not yet run
	StagePaused      StageStatus = "paused"
	StageAborted     StageStatus = "aborted"
	StageUnstable    StageStatus = "unstable"
	StageUnknown     StageStatus = "unknown"
)

// Stage is one pipeline stage; Children preserve parallel branches / nested nodes.
type Stage struct {
	ID       string // node id; the key of its console output
	Name     string
	Status   StageStatus
	Duration time.Duration
	Children []Stage
}

func normaliseStage(s string) StageStatus {
	switch strings.ToUpper(s) {
	case "SUCCESS":
		return StageSuccess
	case "FAILED", "FAILURE":
		return StageFailed
	case "IN_PROGRESS":
		return StageRunning
	case "NOT_EXECUTED", "QUEUED", "NOT_BUILT":
		return StageNotExecuted
	case "PAUSED_PENDING_INPUT":
		return StagePaused
	case "ABORTED":
		return StageAborted
	case "UNSTABLE":
		return StageUnstable
	}
	return StageUnknown
}

type wfStage struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
	Duration int64     `json:"durationMillis"`
	Children []wfStage `json:"stageFlowNodes"`
}

func (w wfStage) convert() Stage {
	st := Stage{ID: w.ID, Name: w.Name, Status: normaliseStage(w.Status), Duration: time.Duration(w.Duration) * time.Millisecond}
	for _, ch := range w.Children {
		st.Children = append(st.Children, ch.convert())
	}
	return st
}

// Stages returns the pipeline stages of a build (number 0 = last build) from
// the wfapi. ErrNoStages is returned when wfapi answers 404 (freestyle job or
// plugin missing). wfapi ignores tree=, so none is sent.
func (c *Client) Stages(ctx context.Context, fullName string, number int) ([]Stage, error) {
	var r struct {
		Stages []wfStage `json:"stages"`
	}
	resp, err := c.do(ctx, http.MethodGet, jobPath(fullName)+"/"+buildRef(number)+"/wfapi/describe", &r)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, ErrNoStages
		}
		return nil, err
	}
	out := make([]Stage, 0, len(r.Stages))
	for _, s := range r.Stages {
		out = append(out, s.convert())
	}
	return out, nil
}

// JobStatus is a job's status derived from Jenkins' "color" hint.
type JobStatus string

// Job statuses.
const (
	JobSuccess  JobStatus = "success"
	JobFailed   JobStatus = "failed"
	JobUnstable JobStatus = "unstable"
	JobRunning  JobStatus = "running"
	JobDisabled JobStatus = "disabled"
	JobNotBuilt JobStatus = "notbuilt"
	JobAborted  JobStatus = "aborted"
	JobUnknown  JobStatus = "unknown"
)

// StatusFromColor maps a Jenkins color ("blue", "red_anime", ...) to a JobStatus.
func StatusFromColor(color string) JobStatus {
	if strings.HasSuffix(color, "_anime") {
		return JobRunning
	}
	switch color {
	case "blue", "green":
		return JobSuccess
	case "red":
		return JobFailed
	case "yellow":
		return JobUnstable
	case "disabled":
		return JobDisabled
	case "notbuilt", "grey":
		return JobNotBuilt
	case "aborted":
		return JobAborted
	}
	return JobUnknown
}
