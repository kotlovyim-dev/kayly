package deploy

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
)

// ServerStatus is the host Kayly runs on, as far as an owner deciding whether it is
// healthy needs to know.
type ServerStatus struct {
	DiskUsed      uint64 `json:"disk_used"`
	DiskTotal     uint64 `json:"disk_total"`
	MemoryTotal   uint64 `json:"memory_total"`
	MemoryFree    uint64 `json:"memory_available"`
	DockerVersion string `json:"docker_version,omitempty"`
}

// Server reports the data disk, the memory and the Docker version. A part that cannot be
// read is left at zero rather than failing the rest.
func (d *Deployer) Server(ctx context.Context) ServerStatus {
	var s ServerStatus
	if d.opt.DiskPath != "" {
		if used, total, err := d.diskUsage(d.opt.DiskPath); err == nil {
			s.DiskUsed, s.DiskTotal = used, total
		}
	}
	s.MemoryTotal, s.MemoryFree = readMemInfo("/proc/meminfo")
	if v, err := d.rt.Version(ctx); err == nil {
		s.DockerVersion = v
	}
	return s
}

// readMemInfo returns MemTotal and MemAvailable of a /proc/meminfo-style file in bytes.
func readMemInfo(path string) (total, available uint64) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		fields := strings.Fields(rest)
		if !ok || len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total = kb << 10
		case "MemAvailable":
			available = kb << 10
		}
	}
	return total, available
}
