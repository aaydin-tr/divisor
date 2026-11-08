package run

import (
	"slices"
	"testing"
)

func TestMemoryPeakFileCandidatesCoverDockersSystemdAndCgroupfsDrivers(t *testing.T) {
	containerID := "915a3edb3ec33f1c980c39c6040400a223326f80254367c5644cc466e28a59ea"

	want := []string{
		"/sys/fs/cgroup/system.slice/docker-915a3edb3ec33f1c980c39c6040400a223326f80254367c5644cc466e28a59ea.scope/memory.peak",
		"/sys/fs/cgroup/docker/915a3edb3ec33f1c980c39c6040400a223326f80254367c5644cc466e28a59ea/memory.peak",
	}
	if got := memoryPeakFileCandidates(containerID); !slices.Equal(got, want) {
		t.Errorf("candidates:\n%v\nwant:\n%v", got, want)
	}
}
