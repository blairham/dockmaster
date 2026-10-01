//go:build !darwin && !linux

package engines

// HostMemory is unknown on this platform.
func HostMemory() int64 { return 0 }
