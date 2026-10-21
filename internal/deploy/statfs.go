//go:build linux || darwin

package deploy

import (
	"fmt"
	"syscall"
)

// statfsUsage is the used and total bytes of the filesystem holding path.
func statfsUsage(path string) (used, total uint64, err error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	total = s.Blocks * uint64(s.Bsize)
	return total - s.Bfree*uint64(s.Bsize), total, nil
}
