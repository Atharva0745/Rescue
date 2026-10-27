//go:build !linux

package cell

import (
	"context"
)

type Manager struct{}

type Cell struct{}

func newManager(string, Config) (*Manager, error) {
	return nil, ErrUnsupported
}

func (*Manager) start(string, CommandSpec) (*Cell, error) {
	return nil, ErrUnsupported
}

func (*Cell) wait(context.Context) (Result, error) {
	return Result{}, ErrUnsupported
}

func (*Cell) stop(context.Context) error {
	return ErrUnsupported
}

func (*Cell) close() error {
	return ErrUnsupported
}

func (*Cell) id() string {
	return ""
}

func (*Cell) path() string {
	return ""
}

func (*Cell) pid() int {
	return 0
}

func (*Cell) events() []Event {
	return nil
}

func (*Manager) close(context.Context) error {
	return ErrUnsupported
}
