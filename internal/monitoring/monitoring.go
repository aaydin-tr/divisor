package monitoring

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
)

type Monitoring struct {
	Cpu            CPUStats `json:"cpu"`
	Memory         MemStats `json:"memory"`
	TotalGoroutine int      `json:"total_goroutine"`
}

type CPUStats struct {
	ProcessPercent float64 `json:"process_percent"`
	TotalPercent   float64 `json:"total_percent"`
}

type MemStats struct {
	ProcessPercent float64 `json:"process_percent"`
	TotalPercent   float64 `json:"total_percent"`
	TotalMB        float64 `json:"total_mb"`
}

var once sync.Once
var pid int

func HealthChecker() Monitoring {
	once.Do(func() {
		pid = os.Getpid()
	})

	monitoring := Monitoring{}
	// Get the process ID of the Go program
	// Get the Process object for the Go program
	process, err := process.NewProcess(int32(pid))
	if err != nil {
		fmt.Println(err)
		return Monitoring{}
	}

	processCpuUsage, _ := process.CPUPercent()
	monitoring.Cpu.ProcessPercent = processCpuUsage

	totalCpuUsage, err := cpu.Percent(0, false)
	if err != nil {
		fmt.Println(err)
		return Monitoring{}
	}

	monitoring.Cpu.TotalPercent = totalCpuUsage[0]

	me, _ := mem.VirtualMemory()
	monitoring.Memory.TotalMB = float64(ByteToMB(me.Total))
	a, _ := process.MemoryPercent()
	monitoring.Memory.ProcessPercent = float64(a * float32(ByteToMB(me.Total)) / 100)
	monitoring.Memory.TotalPercent = (float64(me.Used) / float64((me.Total))) * 100
	monitoring.TotalGoroutine = runtime.NumGoroutine()

	return monitoring
}

func ByteToMB(b uint64) uint64 {
	return b / 1024 / 1024
}
