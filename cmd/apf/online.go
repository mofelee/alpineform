package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	corebackend "github.com/mofelee/alpineform/internal/core/backend"
	coreengine "github.com/mofelee/alpineform/internal/core/engine"
	coregraph "github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	coremerge "github.com/mofelee/alpineform/internal/core/merge"
	coreparser "github.com/mofelee/alpineform/internal/core/parser"
	coreplan "github.com/mofelee/alpineform/internal/core/plan"
)

const defaultOnlineParallel = 4

type onlineRunner interface {
	corebackend.Runner
	coreengine.FactsReader
}

type onlineRuntime struct {
	Context   context.Context
	Stdin     io.Reader
	NewRunner func(ir.HostSpec) (onlineRunner, error)
	Provider  coreengine.Provider
}

func defaultOnlineRuntime() onlineRuntime {
	return onlineRuntime{
		Context: context.Background(),
		Stdin:   strings.NewReader(""),
		NewRunner: func(host ir.HostSpec) (onlineRunner, error) {
			return corebackend.NewSSHRunner(host, corebackend.SSHOptions{})
		},
		Provider: unavailableProvider{},
	}
}

func (runtime onlineRuntime) normalized() (onlineRuntime, error) {
	if runtime.Context == nil {
		runtime.Context = context.Background()
	}
	if runtime.Stdin == nil {
		runtime.Stdin = strings.NewReader("")
	}
	if runtime.NewRunner == nil {
		return onlineRuntime{}, fmt.Errorf("online command requires an SSH runner factory")
	}
	if runtime.Provider == nil {
		return onlineRuntime{}, fmt.Errorf("online command requires a resource provider")
	}
	return runtime, nil
}

type unavailableProvider struct{}

func (unavailableProvider) Inspect(context.Context, coregraph.Node) (coreengine.ObservedResource, error) {
	return coreengine.ObservedResource{}, fmt.Errorf("no provider is registered for this managed resource")
}

func (unavailableProvider) Apply(context.Context, coreengine.Step) (coreengine.ObservedResource, error) {
	return coreengine.ObservedResource{}, fmt.Errorf("no provider is registered for this managed resource")
}

func (unavailableProvider) Delete(context.Context, coreengine.Step) error {
	return fmt.Errorf("no provider is registered for this managed resource")
}

type onlineConfigFlags struct {
	sources       repeatedFlag
	variableFiles repeatedFlag
	variables     repeatedFlag
}

func (flags *onlineConfigFlags) bind(fs *flag.FlagSet) {
	fs.Var(&flags.sources, "f", "configuration file or directory; may be repeated")
	fs.Var(&flags.variableFiles, "var-file", "explicit .apfvars or .apfvars.json file; may be repeated")
	fs.Var(&flags.variables, "var", "variable value as name=value; may be repeated")
}

func (flags onlineConfigFlags) loader(workingDir string, environ []string) onlineConfigLoader {
	sources := make([]string, len(flags.sources))
	for i, source := range flags.sources {
		sources[i] = resolvePath(workingDir, source)
	}
	variableFiles := make([]string, len(flags.variableFiles))
	for i, path := range flags.variableFiles {
		variableFiles[i] = resolvePath(workingDir, path)
	}
	return onlineConfigLoader{
		workingDir:    workingDir,
		environ:       append([]string(nil), environ...),
		sources:       sources,
		variableFiles: variableFiles,
		variables:     append([]string(nil), flags.variables...),
	}
}

type onlineConfigLoader struct {
	workingDir    string
	environ       []string
	sources       []string
	variableFiles []string
	variables     []string
}

func (loader onlineConfigLoader) discover() ([]string, error) {
	return coreparser.DiscoverConfigFiles(discoveryWorkingDir(loader.workingDir), loader.sources)
}

func (loader onlineConfigLoader) load() ([]string, *coreparser.Config, error) {
	files, err := loader.discover()
	if err != nil {
		return nil, nil, err
	}
	external, err := coreparser.CollectExternalVariableValues(files, loader.environ, loader.variableFiles, loader.variables)
	if err != nil {
		return nil, nil, err
	}
	config, err := coreparser.ParseFilesWithOptions(files, coreparser.ParseOptions{VariableValues: external})
	if err != nil {
		return nil, nil, err
	}
	return files, config, nil
}

type onlineWorkflow struct {
	engine *coreengine.Engine
	build  coreengine.BuildFunc
	files  []string
	ctx    context.Context
}

func newOnlineWorkflow(loader onlineConfigLoader, runtime onlineRuntime, parallel int) (onlineWorkflow, error) {
	if parallel < 1 {
		return onlineWorkflow{}, fmt.Errorf("parallelism must be at least 1")
	}
	runtime, err := runtime.normalized()
	if err != nil {
		return onlineWorkflow{}, err
	}
	files, err := loader.discover()
	if err != nil {
		return onlineWorkflow{}, err
	}
	remote := corebackend.RemoteBackend{NewRunner: func(host ir.HostSpec) (corebackend.Runner, error) {
		return runtime.NewRunner(host)
	}}
	actionEngine := &coreengine.Engine{Backend: remote, Provider: runtime.Provider, Parallel: parallel}
	build := func(ctx context.Context) (*ir.Program, *coregraph.ResourceGraph, error) {
		_, config, err := loader.load()
		if err != nil {
			return nil, nil, err
		}
		targets, err := coremerge.ConnectionTargets(config)
		if err != nil {
			return nil, nil, err
		}
		facts := make(map[string]ir.HostFacts, len(targets))
		for _, host := range targets {
			runner, err := runtime.NewRunner(host)
			if err != nil {
				return nil, nil, fmt.Errorf("create fact reader for host %q: %w", host.Name, err)
			}
			detected, err := coreengine.DiscoverHostFacts(ctx, runner, coreengine.FactDiscoveryOptions{})
			if err != nil {
				return nil, nil, fmt.Errorf("discover facts for host %q: %w", host.Name, err)
			}
			facts[host.Name] = detected
		}
		program, err := coremerge.CompileWithOptions(config, coremerge.CompileOptions{HostFacts: facts})
		if err != nil {
			return nil, nil, err
		}
		resourceGraph, err := coregraph.Compile(program)
		if err != nil {
			return nil, nil, err
		}
		return program, resourceGraph, nil
	}
	return onlineWorkflow{engine: actionEngine, build: build, files: files, ctx: runtime.Context}, nil
}

func runOnlinePlan(loader onlineConfigLoader, stdout io.Writer, format string, htmlPath string, color bool, parallel int, runtime onlineRuntime) error {
	workflow, err := newOnlineWorkflow(loader, runtime, parallel)
	if err != nil {
		return err
	}
	actionPlan, err := workflow.engine.Plan(workflow.ctx, workflow.build)
	if err != nil {
		return err
	}
	document := coreplan.NewOnline(actionPlan, coreplan.Options{Files: workflow.files})
	if htmlPath != "" {
		if err := ensureOutputDoesNotReplaceInput(htmlPath, workflow.files); err != nil {
			return err
		}
		if err := writePlanHTML(htmlPath, document); err != nil {
			return err
		}
	}
	return printOnlineDocument(stdout, document, format, color)
}

func printOnlineDocument(stdout io.Writer, document coreplan.Document, format string, color bool) error {
	if format == "json" {
		return coreplan.PrintJSON(stdout, document)
	}
	coreplan.PrintText(stdout, document, coreplan.TextOptions{Color: color})
	return nil
}

func runCheck(args []string, stdout io.Writer, workingDir string, environ []string) error {
	return runCheckWithRuntime(args, stdout, workingDir, environ, defaultOnlineRuntime())
}

func runCheckWithRuntime(args []string, stdout io.Writer, workingDir string, environ []string, runtime onlineRuntime) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var configFlags onlineConfigFlags
	configFlags.bind(fs)
	format := fs.String("format", "text", "output format: text or json")
	colorMode := fs.String("color", "auto", "color mode: auto, always, or never")
	parallel := fs.Int("parallel", defaultOnlineParallel, "maximum concurrent hosts")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("check arguments: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("check does not accept positional arguments")
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("unsupported check format %q; use text or json", *format)
	}
	color, err := resolveColor(*colorMode, stdout, environ)
	if err != nil {
		return err
	}
	workflow, err := newOnlineWorkflow(configFlags.loader(workingDir, environ), runtime, *parallel)
	if err != nil {
		return err
	}
	actionPlan, checkErr := workflow.engine.Check(workflow.ctx, workflow.build)
	document := coreplan.NewOnline(actionPlan, coreplan.Options{Files: workflow.files})
	if err := printOnlineDocument(stdout, document, *format, color); err != nil {
		return err
	}
	return checkErr
}

func runApply(args []string, stdout io.Writer, workingDir string, environ []string) error {
	runtime := defaultOnlineRuntime()
	runtime.Stdin = strings.NewReader("")
	return runApplyWithRuntime(args, stdout, workingDir, environ, runtime)
}

func runApplyWithRuntime(args []string, stdout io.Writer, workingDir string, environ []string, runtime onlineRuntime) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var configFlags onlineConfigFlags
	configFlags.bind(fs)
	autoApprove := fs.Bool("auto-approve", false, "approve preview and locked plans")
	_ = fs.Bool("debug", false, "emit detailed operation events")
	lockTimeout := fs.Duration("lock-timeout", 30*time.Second, "maximum time to wait for each host lock")
	colorMode := fs.String("color", "auto", "color mode: auto, always, or never")
	parallel := fs.Int("parallel", defaultOnlineParallel, "maximum concurrent hosts")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("apply arguments: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("apply does not accept positional arguments")
	}
	if *lockTimeout < 0 {
		return fmt.Errorf("lock timeout must not be negative")
	}
	color, err := resolveColor(*colorMode, stdout, environ)
	if err != nil {
		return err
	}
	runtime, err = runtime.normalized()
	if err != nil {
		return err
	}
	workflow, err := newOnlineWorkflow(configFlags.loader(workingDir, environ), runtime, *parallel)
	if err != nil {
		return err
	}
	input := bufio.NewReader(runtime.Stdin)
	var reviewMu sync.Mutex
	review := func(label string, actionPlan coreengine.Plan) error {
		reviewMu.Lock()
		defer reviewMu.Unlock()
		fmt.Fprintln(stdout, label)
		document := coreplan.NewOnline(actionPlan, coreplan.Options{Files: workflow.files})
		if err := printOnlineDocument(stdout, document, "text", color); err != nil {
			return err
		}
		if *autoApprove {
			return nil
		}
		return confirmPlan(input, stdout)
	}
	actual, err := workflow.engine.Apply(workflow.ctx, workflow.build, coreengine.ApplyOptions{
		LockTimeout: *lockTimeout,
		Parallel:    *parallel,
		ReviewPreview: func(_ context.Context, plan coreengine.Plan) error {
			return review("Preview before lock:", plan)
		},
		ReviewLocked: func(_ context.Context, _, locked coreengine.Plan, changed bool) error {
			label := "Locked execution plan:"
			if changed {
				label = "Locked execution plan changed; review the replacement plan:"
			}
			return review(label, locked)
		},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Apply complete: %d host(s).\n", len(actual.Hosts))
	return nil
}

func confirmPlan(input *bufio.Reader, output io.Writer) error {
	fmt.Fprint(output, "Approve this plan? Type yes: ")
	answer, err := input.ReadString('\n')
	if err != nil && len(answer) == 0 {
		return fmt.Errorf("plan approval canceled: %w", err)
	}
	if strings.TrimSpace(answer) != "yes" {
		return fmt.Errorf("plan was not approved")
	}
	return nil
}
