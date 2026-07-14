host "cihost" {
  ssh {
    host          = "__APF_VM_HOST__"
    identity_file = "${path.module}/id_ed25519"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  nftables {
    table "edge" {
      family = "inet"
      content = <<-NFT
        chain input {
          type filter hook input priority 0; policy accept;
          ct state established,related counter accept
          tcp dport 22 counter accept comment "alpineform-v2"
        }
      NFT
      rollback_timeout = "10s"
      on_remove         = "delete"
    }
  }
}
