package state

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
)

// MoveTarget describes the only stored configuration identity that a move may
// update. The paired digests let callers distinguish an address-only change
// from a real desired-state change.
type MoveTarget struct {
	LegacyDesiredDigest string
	TargetDesiredDigest string
}

type RealizedMove struct {
	Host string `json:"host"`
	From string `json:"from"`
	To   string `json:"to"`
}

type MoveResult struct {
	State    State
	Moves    []RealizedMove
	Bindings map[string]ComponentIdentity
}

// ResolveMoves applies component-root moves to an in-memory copy of state.
// It never increments the serial or mutates the input.
func ResolveMoves(st State, declarations []ir.MovedSpec, desiredComponents map[string]bool, targets map[string]MoveTarget) (MoveResult, error) {
	normalized, err := Normalize(st, st.Host)
	if err != nil {
		return MoveResult{}, err
	}
	resolved := pruneComponentIdentities(cloneState(normalized))
	ordered, err := validateAndOrderMoves(resolved.Host, declarations)
	if err != nil {
		return MoveResult{}, err
	}
	finalTargets := finalMoveTargets(ordered)
	result := MoveResult{State: resolved}

	for _, declaration := range ordered {
		sourceAddresses := matchingAddresses(result.State.Resources, declaration.From)
		targetAddresses := matchingAddresses(result.State.Resources, declaration.To)
		if len(sourceAddresses) > 0 && len(targetAddresses) > 0 {
			return MoveResult{}, moveDiagnostic(
				declaration.FromSource,
				"cannot move component root %s to %s because both roots have tracked state",
				declaration.From,
				declaration.To,
			)
		}
		if len(sourceAddresses) == 0 {
			continue
		}

		finalTarget := finalTargets[declaration.From]
		sourceDesired := desiredComponents[declaration.From]
		targetDesired := desiredComponents[finalTarget]
		if sourceDesired {
			if targetDesired {
				return MoveResult{}, moveDiagnostic(
					declaration.FromSource,
					"%s has tracked state while both source and destination components are desired",
					declaration.From,
				)
			}
			continue
		}
		if !targetDesired {
			return MoveResult{}, moveDiagnostic(
				declaration.FromSource,
				"%s has tracked state but destination component %s is not present in the desired graph",
				declaration.From,
				finalTarget,
			)
		}

		physicalName := physicalComponentName(result.State, declaration.From)
		for _, from := range sourceAddresses {
			to := replaceAddressPrefix(from, declaration.From, declaration.To)
			resource := cloneResource(result.State.Resources[from])
			if target, exists := targets[to]; exists {
				resource, err = applyMoveTarget(resource, to, target)
				if err != nil {
					return MoveResult{}, moveDiagnostic(declaration.ToSource, "%v", err)
				}
			}
			delete(result.State.Resources, from)
			result.State.Resources[to] = resource
			result.Moves = append(result.Moves, RealizedMove{Host: result.State.Host, From: from, To: to})
		}
		rebaseResourceDependencies(result.State.Resources, declaration.From, declaration.To)

		delete(result.State.ComponentIdentities, declaration.From)
		targetName, _ := componentNameFromRoot(result.State.Host, declaration.To)
		if physicalName == targetName {
			delete(result.State.ComponentIdentities, declaration.To)
		} else {
			result.State.ComponentIdentities[declaration.To] = ComponentIdentity{PhysicalName: physicalName}
		}
	}

	result.State = pruneComponentIdentities(result.State)
	result.Bindings, err = bindComponentIdentities(result.State, desiredComponents)
	if err != nil {
		return MoveResult{}, err
	}
	return result, nil
}

// BindComponentIdentities resolves the physical name for every desired logical
// component and rejects aliases that would own the same physical namespace.
func BindComponentIdentities(st State, desiredComponents map[string]bool) (map[string]ComponentIdentity, error) {
	normalized, err := Normalize(st, st.Host)
	if err != nil {
		return nil, err
	}
	return bindComponentIdentities(pruneComponentIdentities(normalized), desiredComponents)
}

// PruneComponentIdentities removes physical-name overrides only after their
// logical component root has no remaining tracked resources.
func PruneComponentIdentities(st State) (State, error) {
	normalized, err := Normalize(st, st.Host)
	if err != nil {
		return State{}, err
	}
	return pruneComponentIdentities(normalized), nil
}

func validateAndOrderMoves(host string, declarations []ir.MovedSpec) ([]ir.MovedSpec, error) {
	moves := append([]ir.MovedSpec(nil), declarations...)
	sort.SliceStable(moves, func(i, j int) bool {
		if moves[i].From != moves[j].From {
			return moves[i].From < moves[j].From
		}
		return moves[i].To < moves[j].To
	})

	bySource := make(map[string]ir.MovedSpec, len(moves))
	byTarget := make(map[string]ir.MovedSpec, len(moves))
	validated := make([]ir.MovedSpec, 0, len(moves))
	for _, move := range moves {
		if _, ok := componentNameFromRoot(host, move.From); !ok {
			return nil, moveDiagnostic(move.FromSource, "move source %s is not a component root for host %q", move.From, host)
		}
		if _, ok := componentNameFromRoot(host, move.To); !ok {
			return nil, moveDiagnostic(move.ToSource, "move destination %s is not a component root for host %q", move.To, host)
		}
		if move.From == move.To {
			return nil, moveDiagnostic(move.FromSource, "%s cannot move to itself", move.From)
		}
		if previous, exists := bySource[move.From]; exists {
			return nil, moveDiagnostic(move.FromSource, "move source %s is declared more than once; first destination is %s", move.From, previous.To)
		}
		if previous, exists := byTarget[move.To]; exists {
			return nil, moveDiagnostic(move.ToSource, "move sources %s and %s both target %s", previous.From, move.From, move.To)
		}
		for _, previous := range validated {
			if prefixesOverlap(previous.From, move.From) || prefixesOverlap(previous.To, move.To) {
				return nil, moveDiagnostic(move.FromSource, "move %s to %s overlaps mapping %s to %s", move.From, move.To, previous.From, previous.To)
			}
		}
		bySource[move.From] = move
		byTarget[move.To] = move
		validated = append(validated, move)
	}

	depths := make(map[string]int, len(moves))
	visiting := make(map[string]bool, len(moves))
	var depth func(string) (int, error)
	depth = func(source string) (int, error) {
		if value, exists := depths[source]; exists {
			return value, nil
		}
		if visiting[source] {
			move := bySource[source]
			return 0, moveDiagnostic(move.FromSource, "moved mappings contain a cycle through %s", source)
		}
		visiting[source] = true
		value := 1
		if next, exists := bySource[bySource[source].To]; exists {
			nextDepth, err := depth(next.From)
			if err != nil {
				return 0, err
			}
			value += nextDepth
		}
		delete(visiting, source)
		depths[source] = value
		return value, nil
	}

	sources := make([]string, 0, len(bySource))
	for source := range bySource {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		if _, err := depth(source); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(moves, func(i, j int) bool {
		if depths[moves[i].From] != depths[moves[j].From] {
			return depths[moves[i].From] > depths[moves[j].From]
		}
		return moves[i].From < moves[j].From
	})
	return moves, nil
}

func finalMoveTargets(moves []ir.MovedSpec) map[string]string {
	next := make(map[string]string, len(moves))
	for _, move := range moves {
		next[move.From] = move.To
	}
	out := make(map[string]string, len(moves))
	for _, move := range moves {
		target := move.To
		for {
			nextTarget, exists := next[target]
			if !exists {
				break
			}
			target = nextTarget
		}
		out[move.From] = target
	}
	return out
}

func bindComponentIdentities(st State, desiredComponents map[string]bool) (map[string]ComponentIdentity, error) {
	activeRoots := make(map[string]bool, len(desiredComponents)+len(st.ComponentIdentities))
	for logicalRoot, desired := range desiredComponents {
		if !desired {
			continue
		}
		if _, ok := componentNameFromRoot(st.Host, logicalRoot); !ok {
			return nil, fmt.Errorf("desired component root %s is not valid for host %q", logicalRoot, st.Host)
		}
		activeRoots[logicalRoot] = true
	}
	for address := range st.Resources {
		if logicalRoot, ok := componentRootFromAddress(st.Host, address); ok {
			activeRoots[logicalRoot] = true
		}
	}

	roots := sortedTrueKeys(activeRoots)
	physicalOwners := make(map[string]string, len(roots))
	for _, logicalRoot := range roots {
		physicalName := physicalComponentName(st, logicalRoot)
		if previous, exists := physicalOwners[physicalName]; exists && previous != logicalRoot {
			return nil, fmt.Errorf(
				"component roots %s and %s both resolve to physical component name %q",
				previous,
				logicalRoot,
				physicalName,
			)
		}
		physicalOwners[physicalName] = logicalRoot
	}

	bindings := make(map[string]ComponentIdentity)
	for logicalRoot, desired := range desiredComponents {
		if desired {
			bindings[logicalRoot] = ComponentIdentity{PhysicalName: physicalComponentName(st, logicalRoot)}
		}
	}
	return bindings, nil
}

func validateComponentIdentityMappings(st State) error {
	roots := make([]string, 0, len(st.ComponentIdentities))
	for logicalRoot := range st.ComponentIdentities {
		roots = append(roots, logicalRoot)
	}
	sort.Strings(roots)
	physicalOwners := make(map[string]string, len(roots))
	for _, logicalRoot := range roots {
		identity := st.ComponentIdentities[logicalRoot]
		if _, ok := componentNameFromRoot(st.Host, logicalRoot); !ok {
			return fmt.Errorf("state component identity root %s is not valid for host %q", logicalRoot, st.Host)
		}
		if !validComponentName(identity.PhysicalName) {
			return fmt.Errorf("state component identity %s has invalid physical name %q", logicalRoot, identity.PhysicalName)
		}
		if previous, exists := physicalOwners[identity.PhysicalName]; exists && previous != logicalRoot {
			return fmt.Errorf(
				"state component identity roots %s and %s both use physical component name %q",
				previous,
				logicalRoot,
				identity.PhysicalName,
			)
		}
		physicalOwners[identity.PhysicalName] = logicalRoot
	}
	return nil
}

func pruneComponentIdentities(st State) State {
	out := cloneState(st)
	for logicalRoot := range out.ComponentIdentities {
		if len(matchingAddresses(out.Resources, logicalRoot)) == 0 {
			delete(out.ComponentIdentities, logicalRoot)
		}
	}
	return out
}

func physicalComponentName(st State, logicalRoot string) string {
	if identity, exists := st.ComponentIdentities[logicalRoot]; exists {
		return identity.PhysicalName
	}
	name, _ := componentNameFromRoot(st.Host, logicalRoot)
	return name
}

func applyMoveTarget(resource Resource, address string, target MoveTarget) (Resource, error) {
	if (target.LegacyDesiredDigest == "") != (target.TargetDesiredDigest == "") {
		return Resource{}, fmt.Errorf("move target %s must set both legacy and target desired digests", address)
	}
	if target.LegacyDesiredDigest != "" && resource.DesiredDigest == target.LegacyDesiredDigest {
		resource.DesiredDigest = target.TargetDesiredDigest
	}
	return resource, nil
}

func matchingAddresses(resources map[string]Resource, prefix string) []string {
	addresses := make([]string, 0)
	for address := range resources {
		if address == prefix || strings.HasPrefix(address, prefix+".") {
			addresses = append(addresses, address)
		}
	}
	sort.Strings(addresses)
	return addresses
}

func replaceAddressPrefix(address, from, to string) string {
	return to + strings.TrimPrefix(address, from)
}

func rebaseResourceDependencies(resources map[string]Resource, from, to string) {
	for address, resource := range resources {
		dependencies := append([]string(nil), resource.DependsOn...)
		for index, dependency := range dependencies {
			if dependency != from && !strings.HasPrefix(dependency, from+".") {
				continue
			}
			dependencies[index] = replaceAddressPrefix(dependency, from, to)
		}
		resource.DependsOn = canonicalDependencies(dependencies)
		resources[address] = resource
	}
}

func prefixesOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+".") || strings.HasPrefix(right, left+".")
}

func componentNameFromRoot(host, root string) (string, bool) {
	prefix := "host." + host + ".component."
	if !strings.HasPrefix(root, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(root, prefix)
	if !validComponentName(name) {
		return "", false
	}
	return name, true
}

func componentRootFromAddress(host, address string) (string, bool) {
	prefix := "host." + host + ".component."
	if !strings.HasPrefix(address, prefix) {
		return "", false
	}
	remainder := strings.TrimPrefix(address, prefix)
	name := remainder
	if separator := strings.IndexByte(remainder, '.'); separator >= 0 {
		name = remainder[:separator]
	}
	if !validComponentName(name) {
		return "", false
	}
	return prefix + name, true
}

func validComponentName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' {
			continue
		}
		if index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func sortedTrueKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if value {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func moveDiagnostic(source ir.SourceRef, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if source.File == "" {
		return fmt.Errorf("%s", message)
	}
	return fmt.Errorf("%s:%d:%s: %s", source.File, source.Line, source.Path, message)
}
