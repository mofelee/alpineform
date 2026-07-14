package graph

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/mofelee/alpineform/internal/core/ir"
)

type nftablesAddresses struct {
	Table        string
	Package      string
	Persistence  string
	Service      string
	Candidate    string
	Active       string
	Watchdog     string
	Confirmation string
}

func appendNftablesNodes(resourceGraph *ResourceGraph, host ir.HostSpec, hostAddress string) {
	if host.Nftables == nil {
		return
	}
	for _, table := range host.Nftables.Tables {
		addresses := nftablesResourceAddresses(host.Name, table.Family, table.Name)
		deleteBehavior := ""
		if table.OnRemove == "delete" {
			deleteBehavior = "delete"
		}
		contentBytes := table.ContentBytes
		if table.ContentWriteOnly {
			contentBytes = 0
		}
		persistencePath := nftablesPersistencePath(table.Family, table.Name)
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: addresses.Table, Kind: "nftables_table", Managed: true,
			Summary: "manage rollback-safe nftables table " + table.Family + " " + table.Name,
			Source:  table.Source, Lifecycle: &table.Lifecycle,
			Desired: map[string]any{
				"family": table.Family, "name": table.Name, "ownership": "exclusive_named_table",
				"ensure": table.Ensure, "adopt_existing": table.AdoptExisting,
				"rollback_timeout_seconds": table.RollbackTimeoutSeconds,
				"content_sha256":           table.ContentSHA256, "content_bytes": contentBytes,
				"content_version": table.ContentVersion, "content_write_only": table.ContentWriteOnly,
				"persistence_path": persistencePath, "external_rules": "preserve",
				"transaction_protocol": "rollback_watchdog_v1", "delete_behavior": deleteBehavior,
				"delete":          map[string]any{"family": table.Family, "name": table.Name, "persistence_path": persistencePath},
				"prevent_destroy": table.Lifecycle.PreventDestroy,
			},
			Payload: map[string]any{
				"content": table.Content,
				"transaction_addresses": map[string]string{
					"persistence": addresses.Persistence, "candidate": addresses.Candidate, "active": addresses.Active,
					"watchdog": addresses.Watchdog, "confirmation": addresses.Confirmation,
				},
			},
			DependsOn: []string{hostAddress}, Sensitive: true, Ephemeral: table.Ephemeral, DigestSafe: true,
		})
	}
}

func nftablesResourceAddresses(host, family, name string) nftablesAddresses {
	identity := family + "/" + name
	table := "host." + host + ".nftables.table[" + strconv.Quote(identity) + "]"
	return nftablesAddresses{
		Table: table, Package: packageResourceAddress(host, "nftables"), Persistence: table + ".persistence",
		Service: "host." + host + ".nftables.service", Candidate: table + ".transaction.candidate",
		Active: table + ".transaction.active", Watchdog: table + ".transaction.watchdog",
		Confirmation: table + ".transaction.confirmation",
	}
}

func nftablesPersistencePath(family, name string) string {
	return filepath.Join("/etc/nftables.d/alpineform", fmt.Sprintf("%s-%s.nft", family, name))
}
