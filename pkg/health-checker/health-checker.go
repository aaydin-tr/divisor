package healthchecker

import (
	"fmt"
	"os"

	"github.com/shirou/gopsutil/process"
)

func HealthChecker() {
	// Get the process ID of the Go program
	pid := os.Getpid()

	// Get the Process object for the Go program
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		fmt.Println(err)
		return
	}

	// Get the CPU usage of the Go program
	usage, err := p.CPUPercent()
	if err != nil {
		fmt.Println(err)
		return
	}

	// Print the CPU usage
	fmt.Printf("CPU usage: %f%%\n", usage)
}
