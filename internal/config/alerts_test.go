// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
	"time"
)

// TestAlertsDefaults: alerts are on, quiet, and three crash restarts in
// five minutes is a loop.
func TestAlertsDefaults(t *testing.T) {
	want := Alerts{Enabled: true, RestartLoop: RestartLoop{Count: 3, Window: 5 * time.Minute}}
	if got := Default().Alerts; got != want {
		t.Errorf("Default().Alerts = %+v, want %+v", got, want)
	}
}

// TestContextAlertsOverride: a context's alerts entry replaces only the
// keys it sets, down to a restartLoop's count or window alone.
func TestContextAlertsOverride(t *testing.T) {
	got, err := Parse(strings.NewReader(`dockmaster:
  alerts:
    bell: true
    restartLoop:
      count: 4
      window: 2m
  contexts:
    prod:
      alerts:
        enabled: false
    dev:
      alerts:
        bell: false
        restartLoop:
          window: 10m
    colima: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	top := got.Alerts
	cases := []struct {
		ctx  string
		want Alerts
	}{
		{ctx: "prod", want: Alerts{Bell: true, RestartLoop: RestartLoop{Count: 4, Window: 2 * time.Minute}}},
		{
			ctx:  "dev",
			want: Alerts{Enabled: true, RestartLoop: RestartLoop{Count: 4, Window: 10 * time.Minute}},
		},
		{ctx: "colima", want: top},
		{ctx: "absent", want: top},
	}
	for _, tc := range cases {
		if got := top.With(got.Contexts[tc.ctx].Alerts); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.ctx, got, tc.want)
		}
	}
}

// TestAlertsRejects: a restart loop of one restart, or a window under a
// second, is refused, top-level or in a context, naming where.
func TestAlertsRejects(t *testing.T) {
	ctx := "dockmaster:\n  contexts:\n    prod:\n      alerts:\n        restartLoop:\n"
	for in, want := range map[string]string{
		"dockmaster:\n  alerts:\n    restartLoop:\n      count: 1\n":   "alerts.restartLoop.count",
		"dockmaster:\n  alerts:\n    restartLoop:\n      window: 0s\n": "alerts.restartLoop.window",
		"dockmaster:\n  alerts:\n    belll: true\n":                    `unknown key "belll"`,
		ctx + "          count: 1\n":                                   "contexts.prod.alerts.restartLoop.count",
		ctx + "          window: 1ms\n":                                "contexts.prod.alerts.restartLoop.window",
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want an error naming %s", in, err, want)
		}
	}
}
