package provider

import (
	"context"
	"fmt"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
)

type RunnerFactory func(host string) (backend.Runner, error)

type Native struct {
	NewRunner RunnerFactory
}

func (provider Native) Inspect(ctx context.Context, node graph.Node) (engine.ObservedResource, error) {
	runner, err := provider.runner(node.Host)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	switch node.Kind {
	case "file":
		return inspectFile(ctx, runner, node)
	case "directory":
		return inspectDirectory(ctx, runner, node)
	case "group":
		return inspectGroup(ctx, runner, node)
	default:
		return engine.ObservedResource{}, fmt.Errorf("no Alpine provider is registered for resource kind %q", node.Kind)
	}
}

func (provider Native) Apply(ctx context.Context, step engine.Step) (engine.ObservedResource, error) {
	runner, err := provider.runner(step.Host)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	switch step.Node.Kind {
	case "file":
		return applyFile(ctx, runner, step.Node)
	case "directory":
		return applyDirectory(ctx, runner, step.Node)
	case "group":
		return applyGroup(ctx, runner, step.Node)
	default:
		return engine.ObservedResource{}, fmt.Errorf("no Alpine provider is registered for resource kind %q", step.Node.Kind)
	}
}

func (provider Native) Delete(ctx context.Context, step engine.Step) error {
	runner, err := provider.runner(step.Host)
	if err != nil {
		return err
	}
	kind := step.Node.Kind
	if kind == "" && step.Prior != nil {
		kind = step.Prior.Kind
	}
	switch kind {
	case "file":
		return deleteFile(ctx, runner, step)
	case "directory":
		return deleteDirectory(ctx, runner, step)
	case "group":
		return deleteGroup(ctx, runner, step)
	default:
		return fmt.Errorf("no Alpine provider is registered for resource kind %q", kind)
	}
}

func (provider Native) runner(host string) (backend.Runner, error) {
	if provider.NewRunner == nil {
		return nil, fmt.Errorf("Alpine native provider requires a runner factory")
	}
	runner, err := provider.NewRunner(host)
	if err != nil {
		return nil, fmt.Errorf("create Alpine provider runner for host %q: %w", host, err)
	}
	if runner == nil {
		return nil, fmt.Errorf("Alpine provider runner factory returned nil for host %q", host)
	}
	return runner, nil
}
