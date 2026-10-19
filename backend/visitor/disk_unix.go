//go:build linux || darwin

package visitor

import "syscall"

func availableBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	return uint64(stat.Bavail) * uint64(stat.Bsize), err
}
