# Root SSH transport

AlpineForm v0.1 manages targets only through root OpenSSH sessions. A host may
declare an SSH alias/address, explicit port, and identity file. The runner
always passes `-l root`, even when the alias has a different user in SSH
configuration, and rejects non-root DSL values before running a command.

OpenSSH configuration remains active for aliases, proxy jumps, host keys, and
other client behavior. AlpineForm adds batch mode, disables forwarding, and
uses a bounded connect timeout. Remote scripts are passed as one shell-quoted
command; state or resource payloads use stdin.

Set `APF_SSH_CONFIG` to an explicit OpenSSH configuration file when the target
alias must be isolated from the user's default configuration. AlpineForm passes
that path with `ssh -F`; aliases, proxy jumps, and known-host policy in the
selected file remain effective.

Commands can mark stdin and output for debug redaction. Protected failures omit
remote stderr, while ordinary diagnostics retain a bounded stderr excerpt.
Context cancellation terminates the local OpenSSH process.
