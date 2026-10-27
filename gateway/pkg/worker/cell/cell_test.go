package cell

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestManagerRejectsUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux-specific manager validation")
	}
	if _, err := NewManager(t.TempDir(), Config{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("NewManager error = %v, want ErrUnsupported", err)
	}
}

func TestManagerRejectsNonCgroupDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux cgroup filesystem validation")
	}
	if _, err := NewManager(t.TempDir(), Config{}); err == nil {
		t.Fatal("NewManager accepted a regular filesystem directory")
	}
}

func TestCellLifecycleAndTerminationLadder(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux cgroup v2")
	}
	root := os.Getenv("RESCUE_CGROUP_ROOT")
	if root == "" {
		t.Skip("set RESCUE_CGROUP_ROOT to a writable delegated cgroup-v2 directory")
	}
	manager, err := NewManager(root, Config{
		CooperativeGrace: 50 * time.Millisecond,
		TerminateGrace:   50 * time.Millisecond,
		PollInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create cgroup manager: %v", err)
	}

	spec, ready := resistantCommand(t)
	cell, err := manager.Start("ignore-signals", spec)
	_ = spec.Stdout.Close()
	if err != nil {
		_ = ready.Close()
		_ = manager.Close(context.Background())
		t.Fatalf("start resistant task: %v", err)
	}
	awaitChildReady(t, ready)
	if cell.PID() <= 0 {
		t.Fatal("cell has an invalid child PID")
	}
	if err := cell.Stop(context.Background()); err != nil {
		t.Fatalf("stop resistant task: %v", err)
	}
	if _, err := cell.Wait(context.Background()); err != nil {
		t.Fatalf("verify stopped task: %v", err)
	}
	if err := cell.Close(); err != nil {
		t.Fatalf("remove verified cgroup: %v", err)
	}
	events := cell.Events()
	want := []string{"start", "cancel_req", "term", "kill", "handle_confirmed"}
	position := 0
	for _, event := range events {
		if position < len(want) && event.Type == want[position] {
			position++
		}
	}
	if position != len(want) {
		t.Fatalf("termination event sequence = %+v, want ordered events %v", events, want)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("close manager: %v", err)
	}
}

func TestCellDoesNotRemoveUnconfirmedCgroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux cgroup v2")
	}
	root := os.Getenv("RESCUE_CGROUP_ROOT")
	if root == "" {
		t.Skip("set RESCUE_CGROUP_ROOT to a writable delegated cgroup-v2 directory")
	}
	manager, err := NewManager(root, Config{})
	if err != nil {
		t.Fatalf("create cgroup manager: %v", err)
	}
	spec, ready := resistantCommand(t)
	cell, err := manager.Start("cancellation-resistant", spec)
	_ = spec.Stdout.Close()
	if err != nil {
		_ = ready.Close()
		_ = manager.Close(context.Background())
		t.Fatalf("start task: %v", err)
	}
	awaitChildReady(t, ready)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cell.Stop(ctx); err == nil {
		t.Fatal("stop with cancelled verification context unexpectedly succeeded")
	}
	if err := cell.Close(); err == nil {
		t.Fatal("unconfirmed cgroup was removed")
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("manager failed to perform final verified cleanup: %v", err)
	}
}

func TestCellWaitConfirmsNaturalExit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux cgroup v2")
	}
	root := os.Getenv("RESCUE_CGROUP_ROOT")
	if root == "" {
		t.Skip("set RESCUE_CGROUP_ROOT to a writable delegated cgroup-v2 directory")
	}
	manager, err := NewManager(root, Config{})
	if err != nil {
		t.Fatalf("create cgroup manager: %v", err)
	}
	cell, err := manager.Start("natural-exit", CommandSpec{Path: "/bin/true"})
	if err != nil {
		_ = manager.Close(context.Background())
		t.Fatalf("start short-lived task: %v", err)
	}
	result, err := cell.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait for natural exit: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}
	events := cell.Events()
	if len(events) != 2 || events[1].Type != "handle_confirmed" {
		t.Fatalf("natural completion did not emit handle confirmation: %+v", events)
	}
	if err := cell.Close(); err != nil {
		t.Fatalf("close naturally completed cell: %v", err)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("close manager: %v", err)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("manager close should be idempotent: %v", err)
	}
}

func TestCellChildHelper(t *testing.T) {
	if os.Getenv("RESCUE_CELL_CHILD") == "" {
		return
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	_, _ = fmt.Fprintln(os.Stdout, "ready")
	for range signals {
	}
}

func resistantCommand(t *testing.T) (CommandSpec, *os.File) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	ready, output, err := os.Pipe()
	if err != nil {
		t.Fatalf("create child readiness pipe: %v", err)
	}
	return CommandSpec{
		Path:   executable,
		Args:   []string{"-test.run=^TestCellChildHelper$"},
		Env:    append(os.Environ(), "RESCUE_CELL_CHILD=ignore-signals"),
		Stdout: output,
	}, ready
}

func awaitChildReady(t *testing.T, ready *os.File) {
	t.Helper()
	defer ready.Close()
	line, err := bufio.NewReader(ready).ReadString('\n')
	if err != nil {
		t.Fatalf("wait for child readiness: %v", err)
	}
	if line != "ready\n" {
		t.Fatalf("child readiness message = %q, want ready", line)
	}
}
