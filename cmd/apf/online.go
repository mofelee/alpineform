package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
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
	coreprovider "github.com/mofelee/alpineform/internal/core/provider"
	corestate "github.com/mofelee/alpineform/internal/core/state"
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
	Events    debugEventSink
}

type debugEvent struct {
	Phase     string
	Host      string
	Operation string
	Address   string
	Status    string
}

type debugEventSink interface {
	Emit(debugEvent)
}

type debugLogger struct {
	output io.Writer
	mu     *sync.Mutex
}

func (logger debugLogger) Emit(event debugEvent) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	fmt.Fprintf(logger.output, "debug phase=%s host=%q operation=%s address=%q status=%s\n", event.Phase, event.Host, event.Operation, event.Address, event.Status)
}

func emitDebug(sink debugEventSink, event debugEvent) {
	if sink != nil {
		sink.Emit(event)
	}
}

func defaultOnlineRuntime() onlineRuntime {
	sshOptions := defaultSSHOptions()
	return onlineRuntime{
		Context: context.Background(),
		Stdin:   strings.NewReader(""),
		NewRunner: func(host ir.HostSpec) (onlineRunner, error) {
			return corebackend.NewSSHRunner(host, sshOptions)
		},
	}
}

func defaultSSHOptions() corebackend.SSHOptions {
	return corebackend.SSHOptions{ConfigPath: os.Getenv("APF_SSH_CONFIG")}
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

type debugRunner struct {
	host     string
	delegate onlineRunner
	events   debugEventSink
}

func (runner debugRunner) Read(ctx context.Context, command string) (string, error) {
	operation := factOperation(command)
	event := debugEvent{Phase: "facts", Host: runner.host, Operation: operation, Status: "started"}
	emitDebug(runner.events, event)
	output, err := runner.delegate.Read(ctx, command)
	event.Status = debugStatus(err)
	emitDebug(runner.events, event)
	return output, err
}

func (runner debugRunner) Run(ctx context.Context, command corebackend.Command) ([]byte, error) {
	phase := "operation"
	switch {
	case strings.HasPrefix(command.Name, "state."):
		phase = "state"
	case command.Name == "lock.release":
		phase = "cleanup"
	case strings.HasPrefix(command.Name, "lock."):
		phase = "lock"
	}
	event := debugEvent{Phase: phase, Host: runner.host, Operation: command.Name, Status: "started"}
	emitDebug(runner.events, event)
	output, err := runner.delegate.Run(ctx, command)
	event.Status = debugStatus(err)
	emitDebug(runner.events, event)
	return output, err
}

func factOperation(command string) string {
	switch command {
	case "cat /etc/os-release":
		return "os-release"
	case "apk --print-arch":
		return "apk-architecture"
	case "uname -m":
		return "kernel-architecture"
	default:
		return "fixed-read"
	}
}

func debugStatus(err error) string {
	if err != nil {
		return "failed"
	}
	return "completed"
}

type debugProvider struct {
	delegate coreengine.Provider
	events   debugEventSink
}

func (provider debugProvider) Inspect(ctx context.Context, node coregraph.Node) (coreengine.ObservedResource, error) {
	event := debugEvent{Phase: "inspect", Host: node.Host, Operation: "inspect", Address: node.Address, Status: "started"}
	emitDebug(provider.events, event)
	observed, err := provider.delegate.Inspect(ctx, node)
	event.Status = debugStatus(err)
	emitDebug(provider.events, event)
	if err != nil && (node.Sensitive || node.Ephemeral) {
		return coreengine.ObservedResource{}, fmt.Errorf("inspect protected resource %q failed", node.Address)
	}
	return observed, err
}

func (provider debugProvider) Apply(ctx context.Context, step coreengine.Step) (coreengine.ObservedResource, error) {
	event := debugEvent{Phase: "operation", Host: step.Host, Operation: step.Action, Address: step.Address, Status: "started"}
	emitDebug(provider.events, event)
	observed, err := provider.delegate.Apply(ctx, step)
	event.Status = debugStatus(err)
	emitDebug(provider.events, event)
	if err != nil && (step.Node.Sensitive || step.Node.Ephemeral) {
		if message, ok := coreengine.SafeOperationMessage(err); ok {
			return coreengine.ObservedResource{}, coreengine.NewSafeOperationError(message)
		}
		return coreengine.ObservedResource{}, fmt.Errorf("%s protected resource %q failed", step.Action, step.Address)
	}
	return observed, err
}

func (provider debugProvider) Delete(ctx context.Context, step coreengine.Step) error {
	event := debugEvent{Phase: "operation", Host: step.Host, Operation: step.Action, Address: step.Address, Status: "started"}
	emitDebug(provider.events, event)
	err := provider.delegate.Delete(ctx, step)
	event.Status = debugStatus(err)
	emitDebug(provider.events, event)
	if err != nil && (step.Node.Sensitive || step.Node.Ephemeral || priorProtected(step.Prior)) {
		if message, ok := coreengine.SafeOperationMessage(err); ok {
			return coreengine.NewSafeOperationError(message)
		}
		return fmt.Errorf("%s protected resource %q failed", step.Action, step.Address)
	}
	return err
}

func priorProtected(resource *corestate.Resource) bool {
	return resource != nil && (resource.Protected || resource.Sensitive || resource.Ephemeral)
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

type onlineHostRegistry struct {
	mu    sync.RWMutex
	hosts map[string]ir.HostSpec
}

func (registry *onlineHostRegistry) replace(hosts []ir.HostSpec) {
	next := make(map[string]ir.HostSpec, len(hosts))
	for _, host := range hosts {
		next[host.Name] = host
	}
	registry.mu.Lock()
	registry.hosts = next
	registry.mu.Unlock()
}

func (registry *onlineHostRegistry) get(name string) (ir.HostSpec, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	host, exists := registry.hosts[name]
	return host, exists
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
	newRunner := func(host ir.HostSpec) (onlineRunner, error) {
		runner, err := runtime.NewRunner(host)
		if err != nil {
			return nil, err
		}
		if runtime.Events != nil {
			return debugRunner{host: host.Name, delegate: runner, events: runtime.Events}, nil
		}
		return runner, nil
	}
	registry := &onlineHostRegistry{}
	remote := corebackend.RemoteBackend{NewRunner: func(host ir.HostSpec) (corebackend.Runner, error) {
		return newRunner(host)
	}}
	provider := runtime.Provider
	if provider == nil {
		provider = coreprovider.Native{NewRunner: func(hostName string) (corebackend.Runner, error) {
			host, exists := registry.get(hostName)
			if !exists {
				return nil, fmt.Errorf("no compiled SSH identity for provider host %q", hostName)
			}
			return newRunner(host)
		}}
	}
	if runtime.Events != nil {
		provider = debugProvider{delegate: provider, events: runtime.Events}
	}
	actionEngine := &coreengine.Engine{Backend: remote, Provider: provider, Parallel: parallel}
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
			runner, err := newRunner(host)
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
		registry.replace(program.Hosts)
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
	allowNetworkDisruption := fs.Bool("allow-network-disruption", false, "explicitly approve network-disrupting changes")
	debug := fs.Bool("debug", false, "emit detailed operation events")
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
	var outputMu sync.Mutex
	if *debug {
		runtime.Events = debugLogger{output: stdout, mu: &outputMu}
	}
	workflow, err := newOnlineWorkflow(configFlags.loader(workingDir, environ), runtime, *parallel)
	if err != nil {
		return err
	}
	input := bufio.NewReader(runtime.Stdin)
	review := func(label string, actionPlan coreengine.Plan) error {
		outputMu.Lock()
		defer outputMu.Unlock()
		fmt.Fprintln(stdout, label)
		document := coreplan.NewOnline(actionPlan, coreplan.Options{Files: workflow.files})
		if err := printOnlineDocument(stdout, document, "text", color); err != nil {
			return err
		}
		if actionPlan.HasNetworkDisruption() && !*allowNetworkDisruption {
			return fmt.Errorf("network-disrupting changes require the explicit --allow-network-disruption option")
		}
		if *autoApprove {
			return nil
		}
		return confirmPlan(input, stdout)
	}
	emitDebug(runtime.Events, debugEvent{Phase: "apply", Operation: "apply", Status: "started"})
	actual, err := workflow.engine.Apply(workflow.ctx, workflow.build, coreengine.ApplyOptions{
		LockTimeout: *lockTimeout,
		Parallel:    *parallel,
		ReviewPreview: func(_ context.Context, plan coreengine.Plan) error {
			event := debugEvent{Phase: "apply", Operation: "preview-review", Status: "started"}
			emitDebug(runtime.Events, event)
			err := review("Preview before lock:", plan)
			event.Status = debugStatus(err)
			emitDebug(runtime.Events, event)
			return err
		},
		ReviewLocked: func(_ context.Context, _, locked coreengine.Plan, changed bool) error {
			host := ""
			if len(locked.Hosts) == 1 {
				host = locked.Hosts[0].Host.Name
			}
			event := debugEvent{Phase: "apply", Host: host, Operation: "locked-review", Status: "started"}
			emitDebug(runtime.Events, event)
			label := "Locked execution plan:"
			if changed {
				label = "Locked execution plan changed; review the replacement plan:"
			}
			err := review(label, locked)
			event.Status = debugStatus(err)
			emitDebug(runtime.Events, event)
			return err
		},
	})
	if err != nil {
		emitDebug(runtime.Events, debugEvent{Phase: "apply", Operation: "apply", Status: "failed"})
		return err
	}
	emitDebug(runtime.Events, debugEvent{Phase: "apply", Operation: "apply", Status: "completed"})
	outputMu.Lock()
	if actual.HasNetworkDisruption() {
		fmt.Fprintln(stdout, "Network-disrupting changes confirmed through the configured management path.")
	}
	fmt.Fprintf(stdout, "Apply complete: %d host(s).\n", len(actual.Hosts))
	outputMu.Unlock()
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
