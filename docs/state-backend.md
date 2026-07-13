# Remote state backend

The default remote state path is `/var/lib/alpineform/state.json`. Reads reject
missing product markers, foreign products, unsupported/newer schemas, and
wrong host identities through the state decoder.

Writes prepare a normalized copy, increment its serial, and encode it before
the backend runner is invoked. The remote script creates a mode `0700` state
directory, writes stdin to a mode `0600` temporary file in the same directory,
and atomically renames that file over the state path. Traps remove incomplete
temporary files on failure or cancellation.

State command stdin and remote error output are marked for redaction. Sensitive
resources persist only a protected marker; ephemeral resources persist neither
values nor their desired digest.
