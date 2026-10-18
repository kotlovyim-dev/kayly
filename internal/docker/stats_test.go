package docker

import (
	"testing"

	"github.com/moby/moby/api/types/container"
)

func TestSummarize(t *testing.T) {
	var st container.StatsResponse
	st.MemoryStats.Usage, st.MemoryStats.Limit = 300<<20, 512<<20
	st.MemoryStats.Stats = map[string]uint64{"inactive_file": 100 << 20}
	st.PreCPUStats.CPUUsage.TotalUsage, st.PreCPUStats.SystemUsage = 1_000, 10_000
	st.CPUStats.CPUUsage.TotalUsage, st.CPUStats.SystemUsage, st.CPUStats.OnlineCPUs = 1_500, 12_000, 4
	got := summarize(st)
	// 500 of 2000 system ticks on 4 cores: a quarter of the machine, one whole core.
	if got.CPUPercent != 100 || got.MemoryBytes != 200<<20 || got.MemoryLimit != 512<<20 {
		t.Fatalf("summary = %+v", got)
	}
	// The kernel can take cache back, so usage never drops below zero when it exceeds usage.
	st.MemoryStats.Stats = map[string]uint64{"total_inactive_file": 400 << 20} // cgroup v1 name
	if got := summarize(st); got.MemoryBytes != 0 {
		t.Fatalf("cache larger than usage: %+v", got)
	}
	// No previous sample: no CPU figure rather than a wrong one.
	st.PreCPUStats.SystemUsage = 0
	st.PreCPUStats.CPUUsage.TotalUsage = 0
	st.CPUStats.SystemUsage = 0
	if got := summarize(st); got.CPUPercent != 0 {
		t.Fatalf("without a delta: %+v", got)
	}
}
