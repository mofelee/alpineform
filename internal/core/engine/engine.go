// Package engine schedules AlpineForm online plan, apply, and check workflows.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

const (
	ActionCreate  = "create"
	ActionUpdate  = "update"
	ActionAdopt   = "adopt"
	ActionDelete  = "delete"
	ActionDestroy = "destroy"
	ActionForget  = "forget"
	ActionNoOp    = "no-op"
)

type ObservedResource struct {
	Exists    bool           `json:"exists"`
	Values    map[string]any `json:"values,omitempty"`
	Digest    string         `json:"digest,omitempty"`
	Protected bool           `json:"protected,omitempty"`
}

func (observed ObservedResource) MarshalJSON() ([]byte, error) {
	type observedJSON ObservedResource
	out := observedJSON(observed)
	if observed.Protected {
		out.Values = nil
		out.Digest = ""
	}
	return json.Marshal(out)
}

type Provider interface {
	Inspect(ctx context.Context, node graph.Node) (ObservedResource, error)
	Apply(ctx context.Context, step Step) (ObservedResource, error)
	Delete(ctx context.Context, step Step) error
}

type Backend interface {
	Read(ctx context.Context, host ir.HostSpec) (corestate.State, error)
	Write(ctx context.Context, host ir.HostSpec, state corestate.State) (corestate.State, error)
	WithLease(ctx context.Context, host ir.HostSpec, timeout time.Duration, work func(context.Context) error) error
}

type BuildFunc func(context.Context) (*ir.Program, *graph.ResourceGraph, error)

type ReviewPreviewFunc func(context.Context, Plan) error
type ReviewLockedFunc func(context.Context, Plan, Plan, bool) error

type ApplyOptions struct {
	LockTimeout   time.Duration
	Parallel      int
	ReviewPreview ReviewPreviewFunc
	ReviewLocked  ReviewLockedFunc
}

type Engine struct {
	Backend  Backend
	Provider Provider
	Parallel int
}

type Plan struct {
	Hosts []HostPlan
}

type HostPlan struct {
	Host        ir.HostSpec
	Steps       []Step
	PriorState  corestate.State
	Fingerprint string
}

type Step struct {
	Host     string
	Address  string
	Action   string
	Summary  string
	Node     graph.Node
	Prior    *corestate.Resource
	Observed ObservedResource
}

func (step Step) MarshalJSON() ([]byte, error) {
	type stepJSON Step
	out := stepJSON(step)
	if step.Node.Sensitive || step.Node.Ephemeral || resourceIsProtected(step.Prior) {
		out.Observed.Protected = true
	}
	return json.Marshal(out)
}

func (plan Plan) HasChanges() bool {
	for _, host := range plan.Hosts {
		for _, step := range host.Steps {
			if step.Action != ActionNoOp {
				return true
			}
		}
	}
	return false
}

func (plan Plan) ForHost(name string) (Plan, bool) {
	for _, host := range plan.Hosts {
		if host.Host.Name == name {
			return Plan{Hosts: []HostPlan{host}}, true
		}
	}
	return Plan{}, false
}

func (engine Engine) Plan(ctx context.Context, build BuildFunc) (Plan, error) {
	if err := engine.validate(); err != nil {
		return Plan{}, err
	}
	if build == nil {
		return Plan{}, fmt.Errorf("online engine requires a build callback")
	}
	program, resourceGraph, err := build(ctx)
	if err != nil {
		return Plan{}, err
	}
	return engine.planBuilt(ctx, program, resourceGraph, "")
}

func (engine Engine) Check(ctx context.Context, build BuildFunc) (Plan, error) {
	plan, err := engine.Plan(ctx, build)
	if err != nil {
		return Plan{}, err
	}
	if plan.HasChanges() {
		return plan, fmt.Errorf("remote resources have drift or unapplied changes")
	}
	return plan, nil
}

func (engine Engine) Apply(ctx context.Context, build BuildFunc, options ApplyOptions) (Plan, error) {
	if options.ReviewPreview == nil || options.ReviewLocked == nil {
		return Plan{}, fmt.Errorf("apply requires preview and locked-plan review callbacks")
	}
	parallel, err := engine.parallelism(options.Parallel)
	if err != nil {
		return Plan{}, err
	}
	preview, err := engine.Plan(ctx, build)
	if err != nil {
		return Plan{}, err
	}
	if err := options.ReviewPreview(ctx, preview); err != nil {
		return preview, err
	}
	actualHosts := make([]HostPlan, len(preview.Hosts))
	completed := make([]bool, len(preview.Hosts))
	err = runBounded(ctx, len(preview.Hosts), parallel, func(workContext context.Context, index int) error {
		previewHost := preview.Hosts[index]
		hostName := previewHost.Host.Name
		err := engine.Backend.WithLease(workContext, previewHost.Host, options.LockTimeout, func(leaseContext context.Context) error {
			program, resourceGraph, err := build(leaseContext)
			if err != nil {
				return err
			}
			if program == nil || resourceGraph == nil {
				return fmt.Errorf("online plan build returned nil program or graph")
			}
			if !reflect.DeepEqual(planHostNames(preview), programHostNames(program)) {
				return fmt.Errorf("host set changed while locks were being acquired; retry apply")
			}
			locked, err := engine.planBuilt(leaseContext, program, resourceGraph, hostName)
			if err != nil {
				return err
			}
			if len(locked.Hosts) != 1 {
				return fmt.Errorf("locked plan did not contain exactly host %q", hostName)
			}
			if !reflect.DeepEqual(previewHost.Host.SSH, locked.Hosts[0].Host.SSH) || !reflect.DeepEqual(previewHost.Host.State, locked.Hosts[0].Host.State) {
				return fmt.Errorf("host %q SSH or state identity changed after lock acquisition; retry apply", hostName)
			}
			previewSingle := Plan{Hosts: []HostPlan{previewHost}}
			changed := previewHost.Fingerprint != locked.Hosts[0].Fingerprint
			if err := options.ReviewLocked(leaseContext, previewSingle, locked, changed); err != nil {
				return err
			}
			if err := engine.executeHost(leaseContext, locked.Hosts[0]); err != nil {
				return err
			}
			actualHosts[index] = locked.Hosts[0]
			completed[index] = true
			return nil
		})
		return err
	})
	actual := Plan{}
	for index, host := range actualHosts {
		if completed[index] {
			actual.Hosts = append(actual.Hosts, host)
		}
	}
	return actual, err
}

func (engine Engine) planBuilt(ctx context.Context, program *ir.Program, resourceGraph *graph.ResourceGraph, hostFilter string) (Plan, error) {
	if program == nil || resourceGraph == nil {
		return Plan{}, fmt.Errorf("online plan build returned nil program or graph")
	}
	scheduled, err := resourceGraph.Schedule()
	if err != nil {
		return Plan{}, fmt.Errorf("schedule online resource graph: %w", err)
	}
	nodesByHost := map[string][]graph.Node{}
	for _, node := range scheduled {
		if node.Managed {
			nodesByHost[node.Host] = append(nodesByHost[node.Host], node)
		}
	}
	hosts := make([]ir.HostSpec, 0, len(program.Hosts))
	for _, host := range program.Hosts {
		if hostFilter != "" && host.Name != hostFilter {
			continue
		}
		hosts = append(hosts, host)
	}
	if hostFilter != "" && len(hosts) == 0 {
		return Plan{}, fmt.Errorf("host %q disappeared while rebuilding the locked plan", hostFilter)
	}
	parallel, err := engine.parallelism(0)
	if err != nil {
		return Plan{}, err
	}
	planned := make([]HostPlan, len(hosts))
	err = runBounded(ctx, len(hosts), parallel, func(workContext context.Context, index int) error {
		host := hosts[index]
		state, err := engine.Backend.Read(workContext, host)
		if err != nil {
			return err
		}
		hostPlan, err := engine.planHost(workContext, host, nodesByHost[host.Name], state)
		if err != nil {
			return err
		}
		planned[index] = hostPlan
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	return Plan{Hosts: planned}, nil
}

func (engine Engine) planHost(ctx context.Context, host ir.HostSpec, nodes []graph.Node, prior corestate.State) (HostPlan, error) {
	hostPlan := HostPlan{Host: host, PriorState: prior}
	current := map[string]bool{}
	plannedActions := map[string]string{}
	for _, node := range nodes {
		current[node.Address] = true
		observed, err := engine.Provider.Inspect(ctx, node)
		if err != nil {
			if node.Sensitive || node.Ephemeral {
				return HostPlan{}, fmt.Errorf("inspect protected resource %q failed", node.Address)
			}
			return HostPlan{}, fmt.Errorf("inspect %s: %w", node.Address, err)
		}
		priorResource, exists := prior.Resources[node.Address]
		step := planNode(node, priorResource, exists, observed)
		if node.Kind == "apk_update" && apkDependenciesChanged(node.DependsOn, plannedActions) {
			if observed.Exists {
				step.Action = ActionUpdate
			} else {
				step.Action = ActionCreate
			}
		}
		if (step.Action == ActionDelete || step.Action == ActionDestroy) && node.Lifecycle != nil && node.Lifecycle.PreventDestroy {
			return HostPlan{}, fmt.Errorf("%s:%d:%s: prevent_destroy blocks %s for %s", node.Source.File, node.Source.Line, node.Source.Path, step.Action, node.Address)
		}
		hostPlan.Steps = append(hostPlan.Steps, step)
		plannedActions[node.Address] = step.Action
	}
	addresses := make([]string, 0, len(prior.Resources))
	for address := range prior.Resources {
		if !current[address] {
			addresses = append(addresses, address)
		}
	}
	sort.Slice(addresses, func(i, j int) bool {
		left := prior.Resources[addresses[i]].Order
		right := prior.Resources[addresses[j]].Order
		if left != right {
			return left > right
		}
		return addresses[i] > addresses[j]
	})
	for _, address := range addresses {
		resource := prior.Resources[address]
		action := ActionForget
		switch resource.DeleteBehavior {
		case ActionDelete:
			action = ActionDelete
		case ActionDestroy:
			action = ActionDestroy
		}
		if resource.PreventDestroy && (action == ActionDelete || action == ActionDestroy) {
			return HostPlan{}, fmt.Errorf("prevent_destroy blocks %s for orphaned state resource %s", action, address)
		}
		copyResource := resource
		hostPlan.Steps = append(hostPlan.Steps, Step{Host: host.Name, Address: address, Action: action, Summary: action + " orphaned state resource", Prior: &copyResource})
	}
	hostPlan.Fingerprint = planFingerprint(hostPlan)
	return hostPlan, nil
}

func apkDependenciesChanged(dependencies []string, actions map[string]string) bool {
	for _, dependency := range dependencies {
		if action := actions[dependency]; action != "" && action != ActionNoOp {
			return true
		}
	}
	return false
}

func planNode(node graph.Node, prior corestate.Resource, hasPrior bool, observed ObservedResource) Step {
	desiredDigest := corestate.Digest(node.Desired)
	observedDigest := observed.Digest
	if observedDigest == "" && observed.Values != nil {
		observedDigest = corestate.Digest(observed.Values)
	}
	observed.Digest = observedDigest
	action := ActionNoOp
	ensure, _ := node.Desired["ensure"].(string)
	if ensure == "absent" {
		if observed.Exists {
			action = ActionDelete
		} else if hasPrior && prior.DesiredDigest != desiredDigest {
			action = ActionAdopt
		}
	} else if !hasPrior {
		switch {
		case !observed.Exists:
			action = ActionCreate
		case desiredBool(node.Desired, "content_write_only"):
			action = ActionUpdate
		case observedDigest == desiredDigest:
			action = ActionAdopt
		default:
			action = ActionUpdate
		}
	} else {
		switch {
		case !observed.Exists:
			action = ActionCreate
		case desiredBool(node.Desired, "content_write_only") && prior.DesiredDigest != desiredDigest:
			action = ActionUpdate
		case observedDigest == desiredDigest && prior.DesiredDigest != desiredDigest:
			action = ActionAdopt
		case prior.DesiredDigest != desiredDigest:
			action = ActionUpdate
		case observedDigest != desiredDigest:
			action = ActionUpdate
		default:
			action = ActionNoOp
		}
	}
	step := Step{Host: node.Host, Address: node.Address, Action: action, Summary: node.Summary, Node: node, Observed: observed}
	if hasPrior {
		copyResource := prior
		step.Prior = &copyResource
	}
	return step
}

func planFingerprint(plan HostPlan) string {
	parts := make([]string, 0, len(plan.Steps)+1)
	parts = append(parts, factsFingerprint(plan.Host.Facts))
	for _, step := range plan.Steps {
		parts = append(parts, strings.Join([]string{step.Address, step.Action, corestate.Digest(step.Node.Desired), step.Observed.Digest, strconvBool(step.Observed.Exists)}, "\x00"))
	}
	return corestate.Digest(parts)
}

func factsFingerprint(facts *ir.HostFacts) string {
	if facts == nil {
		return corestate.Digest(nil)
	}
	semantic := *facts
	semantic.DetectedAt = ""
	return corestate.Digest(semantic)
}

func strconvBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func (engine Engine) executeHost(ctx context.Context, plan HostPlan) error {
	state := copyState(plan.PriorState)
	if plan.Host.Facts != nil {
		facts := *plan.Host.Facts
		state.Facts = &facts
	} else {
		state.Facts = nil
	}
	if state.Resources == nil {
		state.Resources = map[string]corestate.Resource{}
	}
	for index, step := range plan.Steps {
		order := index + 1
		switch step.Action {
		case ActionNoOp:
			if resource, exists := state.Resources[step.Address]; exists {
				resource.Order = order
				state.Resources[step.Address] = resource
			}
			continue
		case ActionForget:
			delete(state.Resources, step.Address)
		case ActionDelete, ActionDestroy:
			if err := engine.Provider.Delete(ctx, step); err != nil {
				if stepIsProtected(step) {
					return fmt.Errorf("%s protected resource %q failed", step.Action, step.Address)
				}
				return fmt.Errorf("%s %s: %w", step.Action, step.Address, err)
			}
			delete(state.Resources, step.Address)
		case ActionAdopt:
			state.Resources[step.Address] = resourceForStep(step, step.Observed, order)
		case ActionCreate, ActionUpdate:
			observed, err := engine.Provider.Apply(ctx, step)
			if err != nil {
				if stepIsProtected(step) {
					return fmt.Errorf("%s protected resource %q failed", step.Action, step.Address)
				}
				return fmt.Errorf("%s %s: %w", step.Action, step.Address, err)
			}
			state.Resources[step.Address] = resourceForStep(step, observed, order)
		default:
			return fmt.Errorf("unsupported action %q for %s", step.Action, step.Address)
		}
	}
	_, err := engine.Backend.Write(ctx, plan.Host, state)
	return err
}

func resourceForStep(step Step, observed ObservedResource, order int) corestate.Resource {
	digest := corestate.Digest(step.Node.Desired)
	observedValues := observed.Values
	if step.Node.Sensitive || step.Node.Ephemeral || observed.Protected {
		observedValues = nil
	}
	if step.Node.Ephemeral && !step.Node.DigestSafe {
		digest = ""
	}
	resource := corestate.Resource{
		Host:          step.Host,
		Kind:          step.Node.Kind,
		Ownership:     "managed",
		Order:         order,
		DesiredDigest: digest,
		Observed:      observedValues,
		Protected:     observed.Protected,
		Sensitive:     step.Node.Sensitive,
		Ephemeral:     step.Node.Ephemeral,
		DigestSafe:    step.Node.DigestSafe,
	}
	if step.Node.Lifecycle != nil {
		resource.PreventDestroy = step.Node.Lifecycle.PreventDestroy
	}
	if behavior, ok := step.Node.Desired["delete_behavior"].(string); ok {
		resource.DeleteBehavior = behavior
	}
	if deletion, ok := step.Node.Desired["delete"].(map[string]any); ok {
		resource.Delete = copyMap(deletion)
	}
	return resource
}

func desiredBool(desired map[string]any, name string) bool {
	value, _ := desired[name].(bool)
	return value
}

func copyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func resourceIsProtected(resource *corestate.Resource) bool {
	return resource != nil && (resource.Protected || resource.Sensitive || resource.Ephemeral)
}

func stepIsProtected(step Step) bool {
	return step.Node.Sensitive || step.Node.Ephemeral || resourceIsProtected(step.Prior)
}

func copyState(input corestate.State) corestate.State {
	out := input
	out.Resources = make(map[string]corestate.Resource, len(input.Resources))
	for address, resource := range input.Resources {
		out.Resources[address] = resource
	}
	if input.Facts != nil {
		facts := *input.Facts
		out.Facts = &facts
	}
	return out
}

func planHostNames(plan Plan) []string {
	names := make([]string, 0, len(plan.Hosts))
	for _, host := range plan.Hosts {
		names = append(names, host.Host.Name)
	}
	sort.Strings(names)
	return names
}

func programHostNames(program *ir.Program) []string {
	names := make([]string, 0, len(program.Hosts))
	for _, host := range program.Hosts {
		names = append(names, host.Name)
	}
	sort.Strings(names)
	return names
}

func SamePlan(left, right Plan) bool {
	return reflect.DeepEqual(left, right)
}

func (engine Engine) validate() error {
	if engine.Backend == nil {
		return errors.New("online engine requires a backend")
	}
	if engine.Provider == nil {
		return errors.New("online engine requires a provider")
	}
	if engine.Parallel < 0 {
		return errors.New("online engine parallelism must not be negative")
	}
	return nil
}

func (engine Engine) parallelism(override int) (int, error) {
	if override < 0 {
		return 0, fmt.Errorf("online engine parallelism must not be negative")
	}
	parallel := override
	if parallel == 0 {
		parallel = engine.Parallel
	}
	if parallel == 0 {
		parallel = 1
	}
	return parallel, nil
}

func runBounded(ctx context.Context, count, parallel int, work func(context.Context, int) error) error {
	if count == 0 {
		return nil
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wait sync.WaitGroup
	var firstErr error
	var firstErrOnce sync.Once
	if parallel > count {
		parallel = count
	}
	for range parallel {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				if err := work(workContext, index); err != nil {
					firstErrOnce.Do(func() {
						firstErr = err
						cancel()
					})
				}
			}
		}()
	}
sendLoop:
	for index := range count {
		select {
		case jobs <- index:
		case <-workContext.Done():
			break sendLoop
		}
	}
	close(jobs)
	wait.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}
