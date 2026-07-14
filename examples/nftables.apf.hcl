host "alpine_nftables" {
  ssh {
    host = "alpine-nftables"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  nftables {
    table "alpineform_filter" {
      family = "inet"
      content = <<-NFT
        chain input {
          type filter hook input priority 0; policy accept;
          ct state established,related accept
          tcp dport 22 accept
        }
      NFT

      rollback_timeout = "30s"
      on_remove         = "forget"

      lifecycle {
        prevent_destroy = true
      }
    }
  }
}
