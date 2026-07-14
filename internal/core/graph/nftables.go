package graph

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
)

const nftablesOpenRCInitScript = `#!/sbin/openrc-run

description="Load AlpineForm-owned nftables tables without flushing external rules"

depend() {
	need localmount
	after nftables
	before net
}

start() {
	local armed=/var/lib/alpineform/nftables/armed
	local directory=/etc/nftables.d/alpineform
	if [ -L "$armed" ] || [ -L "$directory" ]; then
		eerror "Refusing unsafe AlpineForm nftables runtime paths"
		return 1
	fi
	if [ ! -f "$armed" ]; then
		ebegin "Leaving unconfirmed AlpineForm nftables persistence inactive"
		eend 0
		return 0
	fi

	local file
	for file in "$directory"/*.nft; do
		[ -f "$file" ] || continue
		if [ -L "$file" ]; then
			eerror "Refusing symbolic-link AlpineForm nftables persistence"
			return 1
		fi
		local identity=${file##*/}
		local family=${identity%%-*}
		local name=${identity#*-}
		name=${name%.nft}
		if nft list table "$family" "$name" >/dev/null 2>&1; then
			continue
		fi
		nft -c -f "$file" || return 1
	done
	for file in "$directory"/*.nft; do
		[ -f "$file" ] || continue
		[ ! -L "$file" ] || return 1
		local identity=${file##*/}
		local family=${identity%%-*}
		local name=${identity#*-}
		name=${name%.nft}
		if ! nft list table "$family" "$name" >/dev/null 2>&1; then
			nft -f "$file" || return 1
		fi
	done
}

stop() {
	ebegin "Leaving AlpineForm-owned nftables tables active"
	eend 0
}
`

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
	tableAddresses := make([]string, 0, len(host.Nftables.Tables))
	for _, table := range host.Nftables.Tables {
		addresses := nftablesResourceAddresses(host.Name, table.Family, table.Name)
		tableAddresses = append(tableAddresses, addresses.Table)
		deleteBehavior := ""
		if table.OnRemove == "delete" {
			deleteBehavior = "delete"
		}
		contentBytes := table.ContentBytes
		if table.ContentWriteOnly {
			contentBytes = 0
		}
		persistencePath := nftablesPersistencePath(table.Family, table.Name)
		persistenceContent := ""
		persistenceSHA256 := ""
		persistenceBytes := int64(0)
		if table.Ensure == "present" {
			persistenceContent = renderNftablesPersistence(table.Family, table.Name, table.Content)
			sum := sha256.Sum256([]byte(persistenceContent))
			persistenceSHA256 = fmt.Sprintf("%x", sum[:])
			persistenceBytes = int64(len(persistenceContent))
			if table.ContentWriteOnly {
				persistenceSHA256 = ""
				persistenceBytes = 0
			}
		}
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
				"persistence_sha256": persistenceSHA256, "persistence_bytes": persistenceBytes,
				"persistence_owner": "root", "persistence_group": "root", "persistence_mode": "0600",
				"persistence_path": persistencePath, "external_rules": "preserve",
				"transaction_protocol": "rollback_watchdog_v1", "delete_behavior": deleteBehavior,
				"delete":          map[string]any{"family": table.Family, "name": table.Name, "persistence_path": persistencePath},
				"prevent_destroy": table.Lifecycle.PreventDestroy,
			},
			Payload: map[string]any{
				"content": table.Content, "persistence_content": persistenceContent,
				"transaction_addresses": map[string]string{
					"persistence": addresses.Persistence, "candidate": addresses.Candidate, "active": addresses.Active,
					"watchdog": addresses.Watchdog, "confirmation": addresses.Confirmation,
				},
			},
			DependsOn: []string{packageResourceAddress(host.Name, "nftables")}, Sensitive: true, Ephemeral: table.Ephemeral, DigestSafe: true,
		})
	}
	initSum := sha256.Sum256([]byte(nftablesOpenRCInitScript))
	resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
		Host: host.Name, Address: "host." + host.Name + ".nftables.service", Kind: "nftables_service", Managed: true,
		Summary: "install and enable the non-flushing AlpineForm nftables OpenRC service", Source: host.Nftables.Source,
		Desired: map[string]any{
			"name": "alpineform-nftables", "runlevel": "default", "enabled": true, "state": "running",
			"init_path": "/etc/init.d/alpineform-nftables", "init_sha256": fmt.Sprintf("%x", initSum[:]), "init_mode": "0755",
			"persistence_directory": "/etc/nftables.d/alpineform", "persistence_directory_mode": "0700",
			"arming_path": "/var/lib/alpineform/nftables/armed", "stock_configuration": "preserve",
			"ensure": "present", "delete_behavior": "",
		},
		Payload:   map[string]any{"init_script": nftablesOpenRCInitScript},
		DependsOn: append([]string{packageResourceAddress(host.Name, "nftables")}, tableAddresses...), DigestSafe: true,
	})
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

func renderNftablesPersistence(family, name, body string) string {
	trimmed := strings.TrimRight(body, "\n")
	indented := "\t" + strings.ReplaceAll(trimmed, "\n", "\n\t")
	return "# Managed by AlpineForm. Manual changes are repaired.\n" +
		"table " + family + " " + name + " {\n" + indented + "\n}\n"
}
