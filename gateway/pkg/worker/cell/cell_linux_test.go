//go:build linux

package cell

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCellKeepsUnconfirmedCgroupWhenKillFails(t *testing.T) {
	root := os.Getenv("RESCUE_CGROUP_ROOT")
	if root == "" {
		t.Skip("set RESCUE_CGROUP_ROOT to a writable delegated cgroup-v2 directory")
	}
	manager, err := NewManager(root, Config{
		CooperativeGrace: 20 * time.Millisecond,
		TerminateGrace:   20 * time.Millisecond,
		PollInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create cgroup manager: %v", err)
	}
	spec, ready := resistantCommand(t)
	cell, err := manager.Start("kill-failure", spec)
	_ = spec.Stdout.Close()
	if err != nil {
		_ = ready.Close()
		_ = manager.Close(context.Background())
		t.Fatalf("start resistant task: %v", err)
	}
	defer func() {
		manager.kill = writeCgroupKill
		if err := manager.Close(context.Background()); err != nil {
			t.Errorf("cleanup cgroup manager: %v", err)
		}
	}()
	awaitChildReady(t, ready)
	manager.kill = func(string) error {
		return errors.New("injected cgroup kill failure")
	}
	if err := cell.Stop(context.Background()); err == nil {
		t.Fatal("cgroup kill failure was not surfaced")
	}
	if err := cell.Close(); err == nil {
		t.Fatal("cell removed an unconfirmed cgroup after kill failure")
	}
	data, err := os.ReadFile(cell.Path() + "/cgroup.events")
	if err != nil {
		t.Fatalf("read retained cgroup state: %v", err)
	}
	if !strings.Contains(string(data), "populated 1") {
		t.Fatalf("kill failure did not leave the task charged: %s", data)
	}
	manager.kill = writeCgroupKill
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("verified cleanup after restoring cgroup kill: %v", err)
	}
}
