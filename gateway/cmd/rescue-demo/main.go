package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/rescue-gateway/gateway/pkg/controller"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	gateway := controller.NewController(16, 4, 8)
	if err := gateway.RegisterPool("sacrificial", 2); err != nil {
		return fmt.Errorf("register sacrificial pool: %w", err)
	}

	err := gateway.EnqueueWork(controller.WorkRequest{
		TaskID:                 "demo-report-1",
		SourceID:               "demo-source",
		Pool:                   "sacrificial",
		Deadline:               time.Now().Add(2 * time.Second),
		HandleManifestComplete: true,
		Work: func(ctx context.Context) error {
			select {
			case <-time.After(25 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	if err != nil {
		return fmt.Errorf("admit demo work: %w", err)
	}
	if _, err := gateway.DispatchNext(); err != nil {
		return fmt.Errorf("dispatch demo work: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := gateway.WaitTask(ctx, "demo-report-1")
	if err != nil {
		return fmt.Errorf("wait for demo work: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
