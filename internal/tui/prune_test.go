package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func kinds(steps []pruneStep) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.kind)
	}
	return out
}

// TestPruneAllOrder pins `docker system prune`'s order — containers first,
// since removing them frees what the later steps prune — and that volumes
// are only ever included on request, and last.
func TestPruneAllOrder(t *testing.T) {
	a := newTestApp(t)
	if got, want := kinds(
		a.pruneAllSteps(false),
	), []string{
		"containers",
		"networks",
		"images",
		"build cache",
	}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Errorf("prune all = %q, want %q", got, want)
	}
	if got := kinds(a.pruneAllSteps(true)); got[len(got)-1] != "volumes" || len(got) != 5 {
		t.Errorf("prune all volumes = %q, want volumes appended last", got)
	}
	if got := kinds(pruneOnly(a.pruneAllSteps(false), "build cache")); !reflect.DeepEqual(got, []string{"build cache"}) {
		t.Errorf("prune cache = %q", got)
	}
}

// TestRunPruneStepsKeepsGoing: a failing step does not stop the ones after
// it, every step runs in order, and the summary reports counts, singulars
// and the space reclaimed.
func TestRunPruneStepsKeepsGoing(t *testing.T) {
	var ran []string
	step := func(kind string, n int, bytes uint64, err error) pruneStep {
		return pruneStep{kind: kind, run: func(context.Context) (int, uint64, error) {
			ran = append(ran, kind)
			return n, bytes, err
		}}
	}
	summary, err := runPruneSteps(context.Background(), []pruneStep{
		step("containers", 3, 1<<20, nil),
		step("networks", 1, 0, nil),
		step("images", 0, 0, errors.New("conflict: image is in use")),
		step("build cache", 2, 2<<20, nil),
	})
	if !reflect.DeepEqual(ran, []string{"containers", "networks", "images", "build cache"}) {
		t.Errorf("ran %q", ran)
	}
	for _, want := range []string{"3 containers", "1 network", "2 build-cache entries", "reclaimed"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q is missing %q", summary, want)
		}
	}
	if err == nil || !strings.Contains(err.Error(), "images: conflict") {
		t.Errorf("err = %v, want the images failure named", err)
	}
}

// TestPruneCommandsConfirm: every combined prune asks first, says whether
// volumes are included, and declining runs nothing.
func TestPruneCommandsConfirm(t *testing.T) {
	for _, tc := range []struct{ cmd, want string }{
		{cmd: "prune all", want: "volumes are kept"},
		{cmd: "prune all volumes", want: "INCLUDING VOLUMES"},
		{cmd: "prune cache", want: "build cache"},
	} {
		a := newTestApp(t)
		_, cmd := a.dispatchCommand(tc.cmd)
		if cmd != nil {
			t.Errorf(":%s ran something before confirming", tc.cmd)
		}
		if !a.confirm.Active() || !strings.Contains(a.confirm.Prompt(), tc.want) {
			t.Errorf(":%s confirm = %v %q, want %q", tc.cmd, a.confirm.Active(), a.confirm.Prompt(), tc.want)
		}
		if got := step(a, key("n")); got != nil {
			t.Errorf(":%s declined still returned a command", tc.cmd)
		}
	}
}

func TestPruneCommandsReadonly(t *testing.T) {
	a := NewApp(nil, Options{Version: "test", ReadOnly: true})
	for _, cmd := range []string{"prune all", "prune all volumes", "prune cache"} {
		a.errFlash = ""
		a.dispatchCommand(cmd)
		if a.confirm.Active() || !strings.Contains(a.errFlash, "readonly") {
			t.Errorf(":%s not refused in readonly (flash %q)", cmd, a.errFlash)
		}
	}
}
