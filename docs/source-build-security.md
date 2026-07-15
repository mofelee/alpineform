# Target-side source-build security

Target-side component builds are a Preview capability. They execute as root on
the managed Alpine host, so a reviewed build definition and every declared
input are inside the same trust boundary as an AlpineForm configuration.

## Contract and identity

A source component uses `type = "source"`, one `build` block, and one `install`
block. Every build has at least one named input with an exact SHA-256 and a
fixed workspace-relative destination. Inputs come from a controller-local
regular file, inline content, or an HTTP(S) transport locator with no embedded
credentials. The checksum, not a URL or branch name, is the content identity.

Commands are repeated `command` blocks whose `argv` value is a non-empty string
array. AlpineForm never accepts a shell command string and never interpolates
argv into remote shell source. Working directory, input destinations, and the
single declared output are clean relative paths. The first Preview contract
fixes `network = "none"`; undeclared downloads and network-enabled builds are
unsupported.

The build identity covers the resolved component instance, input identities,
argv, protected-value versions, deterministic environment policy, target
platform, APK dependencies, output policy, and install metadata. The graph
uses separate stable addresses for input staging, dependency ownership,
workspace execution, output verification, cleanup, and installation.

## Protected values

Protected inline inputs, environment values, and command stdin require a
public version string. Their bytes stay in provider payloads and redacted SSH
stdin; they are absent from graph, plan, state, HTML, debug events, errors, and
command output. Protected values are never placed in remote shell source or
remote command arguments. Build stdout and stderr are omitted rather than
treated as a safe diagnostic channel.

## Ownership and failure behavior

Declared APK build dependencies belong to one address-derived
`.alpineform-build-*` virtual package and a root-only ownership marker. Cleanup
may remove only that exact owned virtual package. APK retains packages that
remain in world or are required by another package.

Inputs are verified before dependency installation. Commands run in a
deterministic environment and a network namespace. Output verification stages
one regular, non-symlink file in AlpineForm's cache. The prior installation is
not modified by download, dependency, command, missing-output, checksum,
oversize, or cleanup failure. Only the final provider phase copies the verified
cache into the destination filesystem and atomically replaces the target.

Declaration removal defaults to `on_remove = "forget"`. Explicit
`on_remove = "destroy"` records only AlpineForm-owned cache, marker, virtual
package, and installation identities. `lifecycle.prevent_destroy` blocks those
recorded destructive actions before provider execution. A target with matching
AlpineForm build and output markers can be adopted; an unmarked target is never
silently claimed.

## Threat boundaries

- Untrusted source can exploit the compiler, linker, build tools, or kernel.
  Network isolation reduces reach but does not make root compilation safe.
- Path traversal, symlinks, special files, duplicate extracted paths, and
  archive expansion require provider validation before use.
- Cancellation must terminate the owned process group and run bounded cleanup.
  Deterministic ownership markers allow the next apply to recover leftovers.
- Output capture, disk use, output size, and workspace lifetime are bounded;
  resource exhaustion can still make the host unavailable.
- The verified output is still untrusted executable content. AlpineForm proves
  provenance from declared inputs and commands, not semantic safety.

Operators should use dedicated build hosts when the source or toolchain is not
fully trusted. Do not use Preview source builds as a replacement for a
reproducible, isolated release pipeline.
