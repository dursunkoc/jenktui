package jenkins

import (
	"context"
	"errors"
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

// Build is the state of one build.
type Build struct {
	Number    int
	Result    string // SUCCESS, FAILURE, UNSTABLE, ABORTED; "" while running
	Building  bool
	Timestamp time.Time
	Duration  time.Duration
	URL       string
}

func buildRef(number int) string {
	if number <= 0 {
		return "lastBuild"
	}
	return strconv.Itoa(number)
}

// BuildStatus returns a build of a job; number 0 means the last build.
func (c *Client) BuildStatus(ctx context.Context, fullName string, number int) (Build, error) {
	const tree = "number,result,building,timestamp,duration,url"
	var r struct {
		Number    int     `json:"number"`
		Result    *string `json:"result"`
		Building  bool    `json:"building"`
		Timestamp int64   `json:"timestamp"`
		Duration  int64   `json:"duration"`
		URL       string  `json:"url"`
	}
	resp, err := c.do(ctx, http.MethodGet, jobPath(fullName)+"/"+buildRef(number)+"/api/json?tree="+url.QueryEscape(tree), &r)
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
	return b, nil
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
	Name     string    `json:"name"`
	Status   string    `json:"status"`
	Duration int64     `json:"durationMillis"`
	Children []wfStage `json:"stageFlowNodes"`
}

func (w wfStage) convert() Stage {
	st := Stage{Name: w.Name, Status: normaliseStage(w.Status), Duration: time.Duration(w.Duration) * time.Millisecond}
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
