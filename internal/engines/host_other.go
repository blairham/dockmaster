// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

//go:build !darwin && !linux

package engines

// HostMemory is unknown on this platform.
func HostMemory() int64 { return 0 }
