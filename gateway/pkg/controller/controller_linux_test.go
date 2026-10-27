//go:build linux

package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rescue-gateway/gateway/pkg/task"
	"github.com/rescue-gateway/gateway/pkg/worker/cell"
)

func TestCommandWorkRunsInProcessCellAndReconcilesRelease(t *testing.T) {
	root := os.Getenv("RESCUE_CGROUP_ROOT")
	if root == "" {
		t.Skip("set RESCUE_CGROUP_ROOT to a writable delegated cgroup-v2 directory")
	}
	manager, err := cell.NewManager(root, cell.Config{})
	if err != nil {
		t.Fatalf("create cgroup manager: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(context.Background()); err != nil {
			t.Errorf("cleanup process-cell manager: %v", err)
		}
	})
	controller, err := NewControllerWithExecutionCells(2, 2, 2, manager)
	if err != nil {
		t.Fatalf("create controller with process cells: %v", err)
	}
	if err := controller.EnqueueWork(WorkRequest{
		TaskID:                 "closure-not-allowed",
		SourceID:               "closure-source",
		HandleManifestComplete: true,
		Work:                   func(context.Context) error { return nil },
	}); err == nil {
		t.Fatal("process-cell controller accepted closure-backed work")
	}
	if err := controller.RegisterPool("sacrificial", 1); err != nil {
		t.Fatalf("register sacrificial pool: %v", err)
	}
	if err := controller.RegisterPool("normal", 1); err != nil {
		t.Fatalf("register normal pool: %v", err)
	}

	output := filepath.Join(t.TempDir(), "cell-output.txt")
	if err := controller.EnqueueWork(WorkRequest{
		TaskID:                 "process-cell-controller",
		SourceID:               "process-cell-source",
		HandleManifestComplete: true,
		Command: &cell.CommandSpec{
			Path: "/bin/sh",
			Args: []string{"-c", `printf completed > "$1"`, "sh", output},
		},
	}); err != nil {
		t.Fatalf("enqueue command work: %v", err)
	}
	if _, err := controller.DispatchNext(); err != nil {
		t.Fatalf("dispatch command work: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := controller.WaitTask(ctx, "process-cell-controller")
	if err != nil {
		t.Fatalf("wait for command work: %v", err)
	}
	if snapshot.State != task.StateReleased {
		t.Fatalf("command task state = %s, want RELEASED", snapshot.State)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read process output: %v", err)
	}
	if string(data) != "completed" {
		t.Fatalf("process output = %q, want completed", data)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("close process-cell manager: %v", err)
	}
}

func TestCommandWorkIsStoppedBeforeWorkerChargeIsReleased(t *testing.T) {
	root := os.Getenv("RESCUE_CGROUP_ROOT")
	if root == "" {
		t.Skip("set RESCUE_CGROUP_ROOT to a writable delegated cgroup-v2 directory")
	}
	manager, err := cell.NewManager(root, cell.Config{
		CooperativeGrace: 20 * time.Millisecond,
		TerminateGrace:   20 * time.Millisecond,
		PollInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create cgroup manager: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(context.Background()); err != nil {
			t.Errorf("cleanup process-cell manager: %v", err)
		}
	})
	controller, err := NewControllerWithExecutionCells(2, 2, 2, manager)
	if err != nil {
		t.Fatalf("create controller with process cells: %v", err)
	}
	if err := controller.RegisterPool("sacrificial", 1); err != nil {
		t.Fatalf("register sacrificial pool: %v", err)
	}
	if err := controller.RegisterPool("normal", 1); err != nil {
		t.Fatalf("register normal pool: %v", err)
	}

	started := filepath.Join(t.TempDir(), "started.txt")
	const taskID = "process-cell-cancel"
	if err := controller.EnqueueWork(WorkRequest{
		TaskID:                 taskID,
		SourceID:               "process-cell-cancel-source",
		Deadline:               time.Now().Add(10 * time.Second),
		HandleManifestComplete: true,
		Command: &cell.CommandSpec{
			Path: "/bin/sh",
			Args: []string{"-c", `trap '' INT TERM; printf started > "$1"; while :; do sleep 1; done`, "sh", started},
		},
	}); err != nil {
		t.Fatalf("enqueue resistant command work: %v", err)
	}
	if _, err := controller.DispatchNext(); err != nil {
		t.Fatalf("dispatch resistant command work: %v", err)
	}

	startDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(startDeadline) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatalf("process did not start: %v", err)
	}
	if err := controller.CancelTask(taskID); err != nil {
		t.Fatalf("cancel resistant command work: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := controller.WaitTask(ctx, taskID)
	if err != nil {
		t.Fatalf("wait for verified process termination: %v", err)
	}
	if snapshot.State != task.StateReleased {
		t.Fatalf("cancelled command task state = %s, want RELEASED", snapshot.State)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("close process-cell manager: %v", err)
	}
}
