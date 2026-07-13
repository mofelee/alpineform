# APK repositories and keys

Host-level `apk` blocks manage Alpine repository entries and custom signing
keys without performing distribution upgrades:

```hcl
host "edge" {
  platform { version = "3.24.1" }

  apk {
    repository "main" {
      url = "https://dl-cdn.alpinelinux.org/alpine"
    }
    repository "community" {
      url = "https://dl-cdn.alpinelinux.org/alpine"
    }
  }
}
```

The URL is an HTTPS repository root. AlpineForm appends the detected or
offline-declared branch and the `component`, which defaults to the repository
label. An optional `tag` produces APK's `@tag` repository syntax. URLs with
credentials, query strings, fragments, encoded paths, or non-HTTPS schemes
are rejected. Branches must match the supported Alpine 3.24 target branch.

## Repository ownership

`ownership = "managed"` is the default. Each declaration owns only a block
identified by fixed AlpineForm marker comments in `/etc/apk/repositories`.
External repository lines, comments, blank lines, and their relative order are
preserved. Removing a declaration forgets its state and leaves its marked
block untouched; use `ensure = "absent"` to remove that block explicitly.

`ownership = "authoritative"` is an explicit opt-in that replaces the entire
repositories file with the declared present entries. Online plans show the
complete observed and desired files. In this mode, removing a repository from
the declared set intentionally removes it from the owned file.

Repository files are replaced atomically with root ownership and mode `0644`.
The provider refuses symbolic links, non-regular targets, duplicate markers,
and malformed managed blocks.

## Custom keys

Custom public keys use a fixed safe filename under `/etc/apk/keys`, a relative
module source, and a required SHA-256 digest:

```hcl
apk {
  key "vendor-2026.rsa.pub" {
    source = "keys/vendor-2026.rsa.pub"
    sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }
}
```

AlpineForm verifies the source digest during compilation and again before the
atomic remote replacement. Source bytes travel only through SSH stdin. Key
declaration removal defaults to forget; only `ensure = "absent"` deletes the
fixed remote filename. Symbolic links and non-regular key targets are refused.

## Index refresh and safety

The graph orders custom keys before repositories and repositories before one
synthetic APK index refresh. Any create, adopt, update, drift repair, or
explicit deletion among those dependencies causes exactly one quiet
`apk update` for the host. A clean plan performs none, and declaration-only
forgets perform none. The host lease and graph scheduler serialize all of
these APK mutations.

This surface never invokes `apk upgrade`, `apk fix`, changes the target branch,
or accepts package version constraints. Package intent is documented
separately when the `packages` surface is enabled.
