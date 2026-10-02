//go:build unix

package toolchain

import "syscall"

// freeBytes returns the space available to the user on dir's filesystem, or
// -1 when it cannot be determined.
func freeBytes(dir string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
