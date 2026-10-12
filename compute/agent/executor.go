package agent

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/mgdavisxvs/Ocelot/compute/node"
)

// ExecutionAdapter abstracts workload lifecycle operations.
// ProcessAdapter is the Phase 2 implementation (native OS processes).
// DockerAdapter will be added in Phase 2b.
type ExecutionAdapter interface {
	Start(wl node.DesiredWorkload, dataDir string) error
	Stop(workloadID string, grace time.Duration) error
	IsRunning(workloadID string) bool
	RunningIDs() []string
}

// runningProcess tracks a launched workload process.
type runningProcess struct {
	cmd        *exec.Cmd
	workloadID string
	startedAt  time.Time
	logFile    *os.File
	done       chan struct{} // closed by watch() when the process exits
}

// ProcessAdapter launches workloads as native OS processes.
type ProcessAdapter struct {
	mu       sync.RWMutex
	running  map[string]*runningProcess
	eventsCh chan<- node.HealthEvent
}

func NewProcessAdapter(eventsCh chan<- node.HealthEvent) *ProcessAdapter {
	return &ProcessAdapter{
		running:  make(map[string]*runningProcess),
		eventsCh: eventsCh,
	}
}

// Start launches a workload process.
// Working directory: $dataDir/workloads/$workloadID/
// Stdout/stderr: $dataDir/workloads/$workloadID/output.log
func (pa *ProcessAdapter) Start(wl node.DesiredWorkload, dataDir string) error {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	if _, exists := pa.running[wl.ID]; exists {
		return nil // idempotent
	}

	m := wl.Manifest
	if m.Entrypoint == "" {
		return fmt.Errorf("executor: workload %s has no entrypoint", wl.ID)
	}

	workDir := filepath.Join(dataDir, "workloads", wl.ID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("executor: mkdir %s: %w", workDir, err)
	}

	logPath := filepath.Join(workDir, "output.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("executor: open log %s: %w", logPath, err)
	}

	// Build environment: inherit minimal env, then overlay manifest env.
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + workDir,
		"WORKLOAD_ID=" + wl.ID,
	}
	for k, v := range m.Env {
		env = append(env, k+"="+v)
	}

	args := append([]string{m.Entrypoint}, m.Args...)
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec
	cmd.Dir = workDir
	cmd.Env = env
	cmd.Stdout = io.MultiWriter(logFile, os.Stdout)
	cmd.Stderr = io.MultiWriter(logFile, os.Stderr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // new process group for clean kill

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("executor: start %s: %w", wl.ID, err)
	}

	rp := &runningProcess{
		cmd:        cmd,
		workloadID: wl.ID,
		startedAt:  time.Now(),
		logFile:    logFile,
		done:       make(chan struct{}),
	}
	pa.running[wl.ID] = rp

	slog.Info("executor: workload started", "workload_id", wl.ID, "pid", cmd.Process.Pid)

	// Watch the process in the background; watch() is the sole caller of cmd.Wait().
	go pa.watch(rp, wl.ID)

	return nil
}

// watch waits for a process to exit and emits a health event.
// It is the sole caller of cmd.Wait() for a given process.
func (pa *ProcessAdapter) watch(rp *runningProcess, workloadID string) {
	err := rp.cmd.Wait()
	close(rp.done)
	rp.logFile.Close()

	pa.mu.Lock()
	delete(pa.running, workloadID)
	pa.mu.Unlock()

	exitCode := 0
	kind := node.EventCompleted
	msg := ""

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			if exitCode == -1 {
				// Killed by signal.
				kind = node.EventFailed
				msg = fmt.Sprintf("killed by signal: %v", exitErr)
			} else {
				kind = node.EventFailed
				msg = fmt.Sprintf("exited with code %d", exitCode)
			}
		} else {
			kind = node.EventFailed
			msg = err.Error()
		}
	}

	ec := exitCode
	pa.eventsCh <- node.HealthEvent{
		WorkloadID: workloadID,
		Kind:       kind,
		Ts:         nowMs(),
		PID:        rp.cmd.Process.Pid,
		ExitCode:   &ec,
		Message:    msg,
	}

	slog.Info("executor: workload exited",
		"workload_id", workloadID,
		"exit_code", exitCode,
		"kind", kind,
	)
}

// Stop sends SIGTERM then SIGKILL after the grace period.
func (pa *ProcessAdapter) Stop(workloadID string, grace time.Duration) error {
	pa.mu.RLock()
	rp, ok := pa.running[workloadID]
	pa.mu.RUnlock()

	if !ok {
		return nil // already stopped, idempotent
	}

	slog.Info("executor: stopping workload", "workload_id", workloadID, "grace", grace)

	// Send SIGTERM to the entire process group.
	if rp.cmd.Process != nil {
		if err := syscall.Kill(-rp.cmd.Process.Pid, syscall.SIGTERM); err != nil {
			slog.Warn("executor: SIGTERM failed", "workload_id", workloadID, "err", err)
		}
	}

	// Wait for graceful exit (watch() owns cmd.Wait); fall back to SIGKILL.
	select {
	case <-rp.done:
		// watch() completed; process has exited.
	case <-time.After(grace):
		slog.Warn("executor: grace period expired, sending SIGKILL", "workload_id", workloadID)
		if rp.cmd.Process != nil {
			syscall.Kill(-rp.cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck
		}
	}

	return nil
}

func (pa *ProcessAdapter) IsRunning(workloadID string) bool {
	pa.mu.RLock()
	defer pa.mu.RUnlock()
	_, ok := pa.running[workloadID]
	return ok
}

func (pa *ProcessAdapter) RunningIDs() []string {
	pa.mu.RLock()
	defer pa.mu.RUnlock()
	ids := make([]string, 0, len(pa.running))
	for id := range pa.running {
		ids = append(ids, id)
	}
	return ids
}
