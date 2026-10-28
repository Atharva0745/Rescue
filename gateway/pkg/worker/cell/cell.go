package cell

import (
	"context"
	"errors"
	"os"
	"time"
)

var ErrUnsupported = errors.New("process execution cells require Linux cgroup v2")

// Config controls the bounded termination ladder for a process cell.
type Config struct {
	CooperativeGrace time.Duration
	TerminateGrace   time.Duration
	PollInterval     time.Duration
}

func (c Config) withDefaults() Config {
	if c.CooperativeGrace <= 0 {
		c.CooperativeGrace = 250 * time.Millisecond
	}
	if c.TerminateGrace <= 0 {
		c.TerminateGrace = 250 * time.Millisecond
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 10 * time.Millisecond
	}
	return c
}

// CommandSpec describes a child process. Work must be executable as a process;
// an in-process Go closure cannot be moved into a cgroup.
type CommandSpec struct {
	Path   string
	Args   []string
	Env    []string
	Dir    string
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
}

// Result describes the child process after it has exited and its cgroup has
// been independently verified empty.
type Result struct {
	PID        int
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
}

// Event is a lifecycle measurement from an execution cell.
type Event struct {
	TaskID string
	Type   string
	PID    int
	Path   string
	At     time.Time
}

// NewManager requires a writable delegated cgroup-v2 directory. It never
// creates or modifies cgroups outside that directory.
func NewManager(root string, config Config) (*Manager, error) {
	return newManager(root, config.withDefaults())
}

// Start places the process inside its cgroup at process creation, before it
// can execute user code.
func (m *Manager) Start(taskID string, spec CommandSpec) (*Cell, error) {
	return m.start(taskID, spec)
}

// Wait waits for process exit and verifies that the entire cgroup is empty.
func (c *Cell) Wait(ctx context.Context) (Result, error) {
	return c.wait(ctx)
}

// Stop runs cooperative signal, SIGTERM, cgroup.kill, and cgroup-empty
// verification in order.
func (c *Cell) Stop(ctx context.Context) error {
	return c.stop(ctx)
}

// Close removes a cell's cgroup only after its emptiness has been confirmed.
func (c *Cell) Close() error {
	return c.close()
}

// ID returns the externally assigned task ID.
func (c *Cell) ID() string {
	return c.id()
}

// Path returns the cell's cgroup path.
func (c *Cell) Path() string {
	return c.path()
}

// PID returns the child process ID.
func (c *Cell) PID() int {
	return c.pid()
}

// Events returns a copy of the cell's lifecycle events in emission order.
func (c *Cell) Events() []Event {
	return c.events()
}

// Manager.Close stops active cells, confirms they are empty, and removes only
// cgroups created by this manager.
func (m *Manager) Close(ctx context.Context) error {
	return m.close(ctx)
}
