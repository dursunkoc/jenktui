package jenkins

import (
	"context"
	"errors"
	"testing"

	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

func TestStageLog(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("pipe").LastBuild(jenkinstest.Build{Number: 3, Result: "FAILURE"}).Pipeline(
		jenkinstest.Stage{Name: "Build", Status: jenkinstest.StageFailed, Children: []jenkinstest.Stage{
			{Name: "sh", Status: jenkinstest.StageFailed, Log: "+ make <all>\nerror & stop\n"},
		}})
	c := newTestClient(t, s)
	got, err := c.StageLog(context.Background(), "pipe", 3, "101")
	if err != nil || got != "[Pipeline] sh\n+ make <all>\nerror & stop\n" {
		t.Fatalf("step log %q %v", got, err)
	}
	if _, err = c.StageLog(context.Background(), "pipe", 3, "999"); !errors.Is(err, ErrNoStages) {
		t.Fatalf("unknown node: %v", err)
	}
}

func TestConsoleText(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("team/app").LastBuild(jenkinstest.Build{Number: 4, Building: true}).Log("one\ntwo\n")
	c := newTestClient(t, s)
	ctx := context.Background()

	ch, err := c.ConsoleText(ctx, "team/app", 4, 0)
	if err != nil || ch.Text != "one\ntwo\n" || ch.Next != 8 || !ch.More {
		t.Fatalf("first chunk %+v %v", ch, err)
	}
	s.Job("team/app").AppendLog("three\n")
	ch, err = c.ConsoleText(ctx, "team/app", 4, ch.Next)
	if err != nil || ch.Text != "three\n" || ch.Next != 14 {
		t.Fatalf("second chunk %+v %v", ch, err)
	}
	// Number 0 means the last build; a finished build reports no more data.
	s.Job("team/app").LastBuild(jenkinstest.Build{Number: 4, Result: "SUCCESS"})
	ch, err = c.ConsoleText(ctx, "team/app", 0, 14)
	if err != nil || ch.Text != "" || ch.More || ch.Next != 14 {
		t.Fatalf("finished %+v %v", ch, err)
	}
	if _, err = c.ConsoleText(ctx, "team/app", 9, 0); !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("missing build: %v", err)
	}
}
