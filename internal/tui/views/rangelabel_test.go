// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

import (
	"testing"
	"time"
)

func TestRangeLabel(t *testing.T) {
	for d, want := range map[time.Duration]string{
		5 * time.Minute:  "5m",
		time.Hour:        "1h",
		90 * time.Second: "1m30s",
		2 * time.Minute:  "2m",
		2 * time.Hour:    "2h",
		90 * time.Minute: "1h30m",
		45 * time.Second: "45s",
	} {
		if got := rangeLabel(d); got != want {
			t.Errorf("rangeLabel(%v) = %q, want %q", d, got, want)
		}
	}
}
