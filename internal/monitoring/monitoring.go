package monitoring

import (
	"fmt"
	"os"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
)

type Monitoring struct {
	Cpu    CPUStats
	Memory MemStats
}

type CPUStats struct {
	ProcessPercent float64
	TotalPercent   float64
}

type MemStats struct {
	ProcessUsageMB float64
	TotalUsageMB   float64
	TotalMB        float64
}

func HealthChecker() {
	var monitoring = Monitoring{}
	// Get the process ID of the Go program
	pid := os.Getpid()
	// Get the Process object for the Go program
	process, err := process.NewProcess(int32(pid))
	if err != nil {
		fmt.Println(err)
		return
	}

	processCpuUsage, _ := process.CPUPercent()
	monitoring.Cpu.ProcessPercent = processCpuUsage

	totalCpuUsage, err := cpu.Percent(0, false)
	if err != nil {
		fmt.Println(err)
		return
	}
	monitoring.Cpu.TotalPercent = totalCpuUsage[0]

	me, _ := mem.VirtualMemory()
	monitoring.Memory.TotalMB = float64(ByteToMB(me.Total))
	a, _ := process.MemoryPercent()
	monitoring.Memory.ProcessUsageMB = float64(a * float32(ByteToMB(me.Total)) / 100)
	monitoring.Memory.TotalUsageMB = float64(ByteToMB(me.Used))

	fmt.Printf("%+v\n", monitoring)
}

func ByteToMB(b uint64) uint64 {
	return b / 1024 / 1024
}
