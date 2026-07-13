package graph

import (
	"reflect"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestCompileKernelOrdersModulesAndAggregatesRuntimeSysctls(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1),
		Kernel: &ir.KernelSpec{
			Modules: []ir.KernelModuleSpec{{Name: "br_netfilter", Source: source(2)}, {Name: "loop", Source: source(3)}},
			Sysctls: []ir.SysctlSpec{
				{Key: "net.bridge.bridge-nf-call-iptables", Value: "1", ApplyRuntime: true, Source: source(4)},
				{Key: "net.ipv4.ip_forward", Value: "1", ApplyRuntime: true, Source: source(5)},
				{Key: "vm.swappiness", Value: "10", ApplyRuntime: false, Source: source(6)},
			},
			Source: source(2),
		},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	for _, node := range compiled.Nodes {
		nodes[node.Address] = node
	}
	moduleAddresses := []string{`host.node.kernel.module["br_netfilter"]`, `host.node.kernel.module["loop"]`}
	for _, key := range []string{"net.bridge.bridge-nf-call-iptables", "net.ipv4.ip_forward", "vm.swappiness"} {
		node := nodes[sysctlResourceAddress("node", key)]
		want := append([]string{"host.node"}, moduleAddresses...)
		if node.Kind != "sysctl" || !reflect.DeepEqual(node.DependsOn, want) || node.Desired["delete_behavior"] != "delete" {
			t.Fatalf("sysctl node %s = %#v", key, node)
		}
	}
	runtime := nodes["host.node.kernel.sysctl_runtime"]
	wantTriggers := []string{
		sysctlResourceAddress("node", "net.bridge.bridge-nf-call-iptables"),
		sysctlResourceAddress("node", "net.ipv4.ip_forward"),
	}
	if runtime.Kind != "sysctl_runtime" || !reflect.DeepEqual(runtime.DependsOn, wantTriggers) || !reflect.DeepEqual(runtime.TriggeredBy, wantTriggers) {
		t.Fatalf("sysctl runtime node = %#v", runtime)
	}
	entries, ok := runtime.Desired["entries"].([]string)
	wantEntries := []string{"net.bridge.bridge-nf-call-iptables", "1", "net.ipv4.ip_forward", "1"}
	if !ok || !reflect.DeepEqual(entries, wantEntries) {
		t.Fatalf("sysctl runtime entries = %#v", runtime.Desired["entries"])
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	order := map[string]int{}
	for index, node := range ordered {
		order[node.Address] = index
	}
	if order[moduleAddresses[0]] >= order[wantTriggers[0]] || order[wantTriggers[0]] >= order[runtime.Address] {
		t.Fatalf("kernel schedule = %#v", order)
	}
}

func TestCompileKernelWithoutRuntimeSysctlsHasNoAggregateNode(t *testing.T) {
	compiled, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1), Kernel: &ir.KernelSpec{Sysctls: []ir.SysctlSpec{{Key: "vm.swappiness", Value: "1", ApplyRuntime: false, Source: source(2)}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range compiled.Nodes {
		if node.Kind == "sysctl_runtime" {
			t.Fatalf("unexpected runtime aggregate = %#v", node)
		}
	}
}
