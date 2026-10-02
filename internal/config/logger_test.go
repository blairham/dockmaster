// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
)

func TestLoggerDefaultsAreK9s(t *testing.T) {
	c := Default()
	if c.Logger.Buffer != 5000 || c.Logger.SinceSeconds != -1 || c.LiveViewAutoRefresh {
		t.Errorf("defaults: buffer %d sinceSeconds %d live %v", c.Logger.Buffer, c.Logger.SinceSeconds, c.LiveViewAutoRefresh)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("defaults do not validate: %v", err)
	}
}

func TestLoggerValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Config)
		want string
	}{
		{name: "buffer below tail", set: func(c *Config) { c.Logger.Buffer = c.Logger.Tail - 1 }, want: "logger.buffer"},
		{name: "buffer too big", set: func(c *Config) { c.Logger.Buffer = MaxLogTail + 1 }, want: "logger.buffer"},
		{name: "since zero", set: func(c *Config) { c.Logger.SinceSeconds = 0 }, want: "logger.sinceSeconds"},
		{name: "since negative", set: func(c *Config) { c.Logger.SinceSeconds = -5 }, want: "logger.sinceSeconds"},
	} {
		c := Default()
		tc.set(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want it to name %s", tc.name, err, tc.want)
		}
	}
	c := Default()
	c.Logger.SinceSeconds = 300
	c.Logger.Buffer = c.Logger.Tail
	if err := c.Validate(); err != nil {
		t.Errorf("a valid window and a buffer equal to tail: %v", err)
	}
}
