// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package version carries the build stamp. Values are injected at link
// time by the Makefile; the defaults are what a plain `go build` gets.
package version

// Version is the semantic version, set via -ldflags at release time.
var Version = "dev"

// Commit is the short git SHA the binary was built from.
var Commit = "none"

// Date is the build timestamp.
var Date = "unknown"
