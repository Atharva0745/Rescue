//go:build linux

package cell

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const cgroup2SuperMagic = 0x63677270

type Manager struct {
	mu      sync.Mutex
	closeMu sync.Mutex
	root    string
	config  Config
	cells   map[*Cell]struct{}
	closed  bool
	removed bool
	kill    func(string) error
}

type Cell struct {
	mu         sync.Mutex
	stopGate   chan struct{}
	manager    *Manager
	taskID     string
	cgroupPath string
	cmd        *exec.Cmd
	processID  int
	startedAt  time.Time
	done       chan struct{}
	result     Result
	waitErr    error
	verified   bool
	closed     bool
	eventLog   []Event
}

func newManager(root string, config Config) (*Manager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("cgroup root must not be empty")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve cgroup root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat cgroup root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cgroup root %s is not a directory", root)
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(root, &fs); err != nil {
		return nil, fmt.Errorf("inspect cgroup filesystem: %w", err)
	}
	if uint64(fs.Type) != cgroup2SuperMagic {
		return nil, fmt.Errorf("cgroup root %s is not on a cgroup-v2 filesystem", root)
	}
	if err := requireFile(root, "cgroup.controllers"); err != nil {
		return nil, err
	}
	if err := requireFile(root, "cgroup.procs"); err != nil {
		return nil, err
	}
	if err := requireFile(root, "cgroup.events"); err != nil {
		return nil, err
	}
	if err := syscall.Access(root, 2); err != nil {
		return nil, fmt.Errorf("cgroup root %s is not writable: %w", root, err)
	}
	suffix := make([]byte, 12)
	if _, err := rand.Read(suffix); err != nil {
		return nil, fmt.Errorf("generate manager cgroup identity: %w", err)
	}
	managerRoot := filepath.Join(root, "rescue-manager-"+hex.EncodeToString(suffix))
	if err := os.Mkdir(managerRoot, 0750); err != nil {
		return nil, fmt.Errorf("create manager cgroup: %w", err)
	}
	for _, file := range []string{"cgroup.controllers", "cgroup.procs", "cgroup.events", "cgroup.kill"} {
		if err := requireFile(managerRoot, file); err != nil {
			_ = os.Remove(managerRoot)
			return nil, fmt.Errorf("manager cgroup does not support verified termination: %w", err)
		}
	}
	return &Manager{
		root:   managerRoot,
		config: config,
		cells:  make(map[*Cell]struct{}),
		kill:   writeCgroupKill,
	}, nil
}

func (m *Manager) start(taskID string, spec CommandSpec) (*Cell, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("task ID must not be empty")
	}
	if strings.TrimSpace(spec.Path) == "" {
		return nil, fmt.Errorf("command path must not be empty")
	}
	if strings.IndexByte(taskID, 0) >= 0 {
		return nil, fmt.Errorf("task ID contains a NUL byte")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("cgroup manager is closed")
	}

	suffix := make([]byte, 12)
	if _, err := rand.Read(suffix); err != nil {
		return nil, fmt.Errorf("generate cgroup identity: %w", err)
	}
	name := "rescue-" + hex.EncodeToString(suffix)
	cgroupPath := filepath.Join(m.root, name)
	if err := os.Mkdir(cgroupPath, 0750); err != nil {
		return nil, fmt.Errorf("create task cgroup: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(cgroupPath)
	}
	for _, file := range []string{"cgroup.procs", "cgroup.events", "cgroup.kill"} {
		if err := requireFile(cgroupPath, file); err != nil {
			cleanup()
			return nil, fmt.Errorf("task cgroup does not support verified termination: %w", err)
		}
	}
	cgroupFD, err := os.Open(cgroupPath)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("open task cgroup: %w", err)
	}
	defer cgroupFD.Close()

	newCommand := func(attributes *syscall.SysProcAttr) *exec.Cmd {
		cmd := exec.Command(spec.Path, spec.Args...)
		cmd.Dir = spec.Dir
		if spec.Env != nil {
			cmd.Env = append([]string(nil), spec.Env...)
		}
		cmd.Stdin = spec.Stdin
		cmd.Stdout = spec.Stdout
		cmd.Stderr = spec.Stderr
		cmd.SysProcAttr = attributes
		return cmd
	}
	cmd := newCommand(&syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    int(cgroupFD.Fd()),
	})
	if err := cmd.Start(); err != nil {
		if !supportsTracedPlacementFallback(err) {
			cleanup()
			return nil, fmt.Errorf("start process in task cgroup: %w", err)
		}
		cmd = newCommand(&syscall.SysProcAttr{Ptrace: true})
		if err := cmd.Start(); err != nil {
			cleanup()
			return nil, fmt.Errorf("start process for stopped cgroup placement: %w", err)
		}
		if err := placeStoppedProcess(cmd, cgroupPath); err != nil {
			cleanupErr := killAndReapStoppedProcess(cmd)
			cleanup()
			return nil, errors.Join(fmt.Errorf("place stopped process in task cgroup: %w", err), cleanupErr)
		}
	}

	startedAt := time.Now().UTC()
	stopGate := make(chan struct{}, 1)
	stopGate <- struct{}{}
	cell := &Cell{
		manager:    m,
		stopGate:   stopGate,
		taskID:     taskID,
		cgroupPath: cgroupPath,
		cmd:        cmd,
		processID:  cmd.Process.Pid,
		startedAt:  startedAt,
		done:       make(chan struct{}),
		eventLog: []Event{{
			TaskID: taskID,
			Type:   "start",
			PID:    cmd.Process.Pid,
			Path:   cgroupPath,
			At:     startedAt,
		}},
	}
	m.cells[cell] = struct{}{}
	go cell.waitProcess()
	return cell, nil
}

func supportsTracedPlacementFallback(err error) bool {
	return errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.ENOSYS) ||
		errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.EOPNOTSUPP)
}

func placeStoppedProcess(cmd *exec.Cmd, cgroupPath string) error {
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WUNTRACED, nil); err != nil {
		return fmt.Errorf("wait for exec-stop: %w", err)
	}
	if !status.Stopped() {
		return fmt.Errorf("child process did not stop before executing user code: status=%v", status)
	}
	if err := os.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"), []byte(strconv.Itoa(cmd.Process.Pid)), 0200); err != nil {
		return fmt.Errorf("move stopped process into cgroup: %w", err)
	}
	if err := syscall.PtraceDetach(cmd.Process.Pid); err != nil {
		return fmt.Errorf("resume process inside cgroup: %w", err)
	}
	return nil
}

func killAndReapStoppedProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
	_ = syscall.PtraceDetach(cmd.Process.Pid)
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return fmt.Errorf("reap failed cgroup child: %w", err)
		}
	}
	return nil
}

func (c *Cell) waitProcess() {
	err := c.cmd.Wait()
	result := Result{
		PID:        c.processID,
		ExitCode:   c.cmd.ProcessState.ExitCode(),
		StartedAt:  c.startedAt,
		FinishedAt: time.Now().UTC(),
	}
	c.mu.Lock()
	c.result = result
	c.waitErr = err
	close(c.done)
	c.mu.Unlock()
}

func (c *Cell) wait(ctx context.Context) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("wait context must not be nil")
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-c.done:
	}
	if err := c.waitEmpty(ctx); err != nil {
		return Result{}, err
	}
	c.mu.Lock()
	c.markVerifiedLocked()
	result := c.result
	waitErr := c.waitErr
	c.mu.Unlock()
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return result, fmt.Errorf("wait for task process: %w", waitErr)
		}
	}
	return result, nil
}

func (c *Cell) stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("stop context must not be nil")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.stopGate:
	}
	defer func() {
		c.stopGate <- struct{}{}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.waitEmptyIfExited(ctx); err != nil {
		return err
	}
	if c.isVerified() {
		return nil
	}

	if err := c.signalMembers(syscall.SIGINT); err != nil {
		return fmt.Errorf("send cooperative stop signal: %w", err)
	}
	c.recordEvent("cancel_req")
	empty, err := c.waitPhase(ctx, c.manager.config.CooperativeGrace)
	if err != nil {
		return err
	}
	if empty {
		return c.confirmEmpty(ctx)
	}
	if !empty {
		if err := c.signalMembers(syscall.SIGTERM); err != nil {
			return fmt.Errorf("send SIGTERM: %w", err)
		}
		c.recordEvent("term")
		empty, err = c.waitPhase(ctx, c.manager.config.TerminateGrace)
		if err != nil {
			return err
		}
	}
	if !empty {
		if err := c.manager.kill(c.cgroupPath); err != nil {
			return fmt.Errorf("kill task cgroup: %w", err)
		}
		c.recordEvent("kill")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
	}
	return c.confirmEmpty(ctx)
}

func (c *Cell) waitEmptyIfExited(ctx context.Context) error {
	select {
	case <-c.done:
		empty, err := c.isEmpty()
		if err != nil {
			return err
		}
		if empty {
			return c.confirmEmpty(ctx)
		}
	default:
	}
	return nil
}

func (c *Cell) waitPhase(ctx context.Context, grace time.Duration) (bool, error) {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	ticker := time.NewTicker(c.manager.config.PollInterval)
	defer ticker.Stop()
	for {
		empty, err := c.isEmpty()
		if err != nil {
			return false, err
		}
		if empty {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timer.C:
			return false, nil
		case <-ticker.C:
		}
	}
}

func (c *Cell) confirmEmpty(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
	}
	if err := c.waitEmpty(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	c.markVerifiedLocked()
	c.mu.Unlock()
	return nil
}

func writeCgroupKill(path string) error {
	return os.WriteFile(filepath.Join(path, "cgroup.kill"), []byte("1"), 0200)
}

func (c *Cell) signalMembers(signal syscall.Signal) error {
	data, err := os.ReadFile(filepath.Join(c.cgroupPath, "cgroup.procs"))
	if err != nil {
		return fmt.Errorf("read cgroup process list: %w", err)
	}
	var failures []error
	for _, line := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(line)
		if err != nil || pid <= 0 {
			failures = append(failures, fmt.Errorf("invalid PID %q in cgroup process list", line))
			continue
		}
		if err := syscall.Kill(pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
			failures = append(failures, fmt.Errorf("signal process %d: %w", pid, err))
		}
	}
	return errors.Join(failures...)
}

func (c *Cell) waitEmpty(ctx context.Context) error {
	ticker := time.NewTicker(c.manager.config.PollInterval)
	defer ticker.Stop()
	for {
		empty, err := c.isEmpty()
		if err != nil {
			return err
		}
		if empty {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("cgroup %s is still populated; capacity must remain charged: %w", c.cgroupPath, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *Cell) isEmpty() (bool, error) {
	data, err := os.ReadFile(filepath.Join(c.cgroupPath, "cgroup.events"))
	if err != nil {
		return false, fmt.Errorf("read cgroup events: %w", err)
	}
	populated := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "populated" {
			populated = fields[1]
			break
		}
	}
	if populated == "" {
		return false, fmt.Errorf("cgroup.events has no populated field")
	}
	if populated != "0" {
		return false, nil
	}
	procs, err := os.ReadFile(filepath.Join(c.cgroupPath, "cgroup.procs"))
	if err != nil {
		return false, fmt.Errorf("read cgroup process list: %w", err)
	}
	if len(strings.TrimSpace(string(procs))) != 0 {
		return false, nil
	}
	return true, nil
}

func (c *Cell) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	if !c.verified {
		return fmt.Errorf("cannot remove task cgroup before its emptiness is verified")
	}
	if err := os.Remove(c.cgroupPath); err != nil {
		return fmt.Errorf("remove verified task cgroup: %w", err)
	}
	c.closed = true
	c.manager.mu.Lock()
	delete(c.manager.cells, c)
	c.manager.mu.Unlock()
	return nil
}

func (c *Cell) events() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.eventLog...)
}

func (c *Cell) recordEvent(eventType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appendEventLocked(eventType)
}

func (c *Cell) appendEventLocked(eventType string) {
	c.eventLog = append(c.eventLog, Event{
		TaskID: c.taskID,
		Type:   eventType,
		PID:    c.processID,
		Path:   c.cgroupPath,
		At:     time.Now().UTC(),
	})
}

func (c *Cell) markVerifiedLocked() {
	if c.verified {
		return
	}
	c.verified = true
	c.appendEventLocked("handle_confirmed")
}

func (c *Cell) id() string {
	return c.taskID
}

func (c *Cell) path() string {
	return c.cgroupPath
}

func (c *Cell) pid() int {
	return c.processID
}

func (c *Cell) isVerified() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.verified
}

func (m *Manager) close(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("manager close context must not be nil")
	}
	m.closeMu.Lock()
	defer m.closeMu.Unlock()

	m.mu.Lock()
	if m.removed {
		m.mu.Unlock()
		return nil
	}
	cells := make([]*Cell, 0, len(m.cells))
	for cell := range m.cells {
		cells = append(cells, cell)
	}
	m.closed = true
	m.mu.Unlock()

	var failures []error
	for _, cell := range cells {
		if err := cell.stop(ctx); err != nil {
			failures = append(failures, fmt.Errorf("stop task %s: %w", cell.taskID, err))
			continue
		}
		if err := cell.close(); err != nil {
			failures = append(failures, fmt.Errorf("close task %s: %w", cell.taskID, err))
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	if err := os.Remove(m.root); err != nil {
		return fmt.Errorf("remove manager cgroup: %w", err)
	}
	m.mu.Lock()
	m.removed = true
	m.mu.Unlock()
	return nil
}

func requireFile(directory, name string) error {
	info, err := os.Stat(filepath.Join(directory, name))
	if err != nil {
		return fmt.Errorf("required cgroup file %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("required cgroup file %s is not a regular control file", name)
	}
	return nil
}
