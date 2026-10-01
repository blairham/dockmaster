package engines

import "golang.org/x/sys/unix"

// HostMemory is this machine's physical memory in bytes, 0 when unknown.
func HostMemory() int64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(n) //nolint:gosec // physical memory fits in int64
}
