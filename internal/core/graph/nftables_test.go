package graph

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestCompileNftablesUsesCollisionFreeProtectedTransactionNode(t *testing.T) {
	secret := "not-a-real-nftables-secret"
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1),
		Nftables: &ir.NftablesSpec{Tables: []ir.NftablesTableSpec{
			{Name: "edge", Family: "ip", Content: secret, ContentSHA256: strings.Repeat("a", 64), ContentBytes: int64(len(secret)), Ensure: "present", OnRemove: "forget", RollbackTimeoutSeconds: 30, Sensitive: true, Source: source(2)},
			{Name: "edge", Family: "ip6", Ensure: "absent", OnRemove: "delete", RollbackTimeoutSeconds: 45, Sensitive: true, Lifecycle: ir.LifecycleSpec{PreventDestroy: true}, Source: source(3)},
		}, Source: source(2)},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	for _, node := range compiled.Nodes {
		nodes[node.Address] = node
	}
	ipAddresses := nftablesResourceAddresses("node", "ip", "edge")
	ip6Addresses := nftablesResourceAddresses("node", "ip6", "edge")
	if ipAddresses.Table == ip6Addresses.Table || ipAddresses.Active == ip6Addresses.Active {
		t.Fatalf("nftables identities collided: %#v %#v", ipAddresses, ip6Addresses)
	}
	ip := nodes[ipAddresses.Table]
	if ip.Kind != "nftables_table" || !ip.Managed || !ip.Sensitive || ip.Payload["content"] != secret || !reflect.DeepEqual(ip.DependsOn, []string{"host.node"}) {
		t.Fatalf("ip nftables node = %#v", ip)
	}
	if ip.Desired["ownership"] != "exclusive_named_table" || ip.Desired["external_rules"] != "preserve" || ip.Desired["delete_behavior"] != "" {
		t.Fatalf("ip ownership contract = %#v", ip.Desired)
	}
	ip6 := nodes[ip6Addresses.Table]
	if ip6.Desired["ensure"] != "absent" || ip6.Desired["delete_behavior"] != "delete" || ip6.Lifecycle == nil || !ip6.Lifecycle.PreventDestroy {
		t.Fatalf("ip6 delete contract = %#v", ip6)
	}
	transactionAddresses, ok := ip.Payload["transaction_addresses"].(map[string]string)
	if !ok || transactionAddresses["persistence"] != ipAddresses.Persistence || transactionAddresses["watchdog"] != ipAddresses.Watchdog || transactionAddresses["confirmation"] != ipAddresses.Confirmation {
		t.Fatalf("transaction addresses = %#v", ip.Payload["transaction_addresses"])
	}
	data, err := json.Marshal(compiled)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), strings.Repeat("a", 64)) {
		t.Fatalf("graph JSON leaked protected nftables data: %s", data)
	}
}
