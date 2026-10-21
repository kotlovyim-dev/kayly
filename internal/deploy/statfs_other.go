//go:build !(linux || darwin)

package deploy

import (
	"fmt"
	"runtime"
)

// statfsUsage is not available here. The daemon runs on Linux; other systems build only to
// run the CLI.
func statfsUsage(path string) (used, total uint64, err error) {
	return 0, 0, fmt.Errorf("statfs %s: not supported on %s", path, runtime.GOOS)
}
