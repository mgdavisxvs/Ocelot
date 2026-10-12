package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/mgdavisxvs/Ocelot/compute/node"
)

// collectInventory gathers current system resource state.
func collectInventory(agentVersion string) node.InventoryRequest {
	inv := node.InventoryRequest{
		Ts:           nowMs(),
		AgentVersion: agentVersion,
		CPUCores:     runtime.NumCPU(),
		RAMMb:        readTotalRAMMb(),
		StorageGb:    readDiskTotalGb("/"),
		GPUs:         collectGPUs(),
	}
	inv.CPUFreePct = readCPUIdlePct()
	inv.RAMFreeMb = readFreeRAMMb()
	inv.DiskFreeGb = readDiskFreeGb("/")
	inv.WattsCurrent = 0 // Phase 3: IPMI integration
	return inv
}

// readTotalRAMMb reads MemTotal from /proc/meminfo (Linux).
// Falls back to a runtime estimate on other platforms.
func readTotalRAMMb() int {
	val, err := readMeminfoKb("MemTotal")
	if err != nil {
		slog.Debug("inventory: MemTotal fallback", "err", err)
		return 0
	}
	return int(val / 1024)
}

// readFreeRAMMb reads MemAvailable from /proc/meminfo.
func readFreeRAMMb() int {
	val, err := readMeminfoKb("MemAvailable")
	if err != nil {
		return 0
	}
	return int(val / 1024)
}

func readMeminfoKb(field string) (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, field+":") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return 0, fmt.Errorf("malformed meminfo line: %q", line)
		}
		v, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse meminfo %s: %w", field, err)
		}
		return v, nil // value is in kB
	}
	return 0, fmt.Errorf("field %q not found in /proc/meminfo", field)
}

// readCPUIdlePct reads a single-sample idle percentage from /proc/stat.
// This is a snapshot, not an average; good enough for inventory reporting.
func readCPUIdlePct() float64 {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		// cpu user nice system idle iowait irq softirq steal guest guest_nice
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return 0
		}
		var total, idle int64
		for i, f := range fields[1:] {
			v, _ := strconv.ParseInt(f, 10, 64)
			total += v
			if i == 3 { // idle is index 3 (fields[4])
				idle = v
			}
		}
		if total == 0 {
			return 0
		}
		return float64(idle) / float64(total) * 100
	}
	return 0
}

// readDiskTotalGb and readDiskFreeGb use syscall.Statfs.
func readDiskTotalGb(path string) int {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0
	}
	return int(s.Blocks * uint64(s.Bsize) / (1024 * 1024 * 1024))
}

func readDiskFreeGb(path string) int {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0
	}
	return int(s.Bavail * uint64(s.Bsize) / (1024 * 1024 * 1024))
}

// collectGPUs runs nvidia-smi to enumerate GPUs. Returns an empty slice if
// nvidia-smi is absent or fails — the agent operates normally without GPUs.
func collectGPUs() []node.InventoryGPU {
	out, err := exec.Command(
		"nvidia-smi",
		"--query-gpu=index,name,memory.total,memory.free,compute_cap",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		// nvidia-smi absent or failed — not an error for CPU-only nodes.
		return nil
	}

	var gpus []node.InventoryGPU
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := splitCSV(line)
		if len(parts) < 5 {
			continue
		}
		idx, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		model := strings.TrimSpace(parts[1])
		totalMb, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
		freeMb, _ := strconv.Atoi(strings.TrimSpace(parts[3]))
		cudaCap := strings.TrimSpace(parts[4])

		gpus = append(gpus, node.InventoryGPU{
			DeviceIndex: idx,
			Model:       model,
			VRAMMb:      totalMb,
			VRAMFreeMb:  freeMb,
			CUDACap:     cudaCap,
			Health:      node.GPUHealthReady,
		})
	}
	return gpus
}

func splitCSV(line string) []string {
	return strings.Split(line, ", ")
}
