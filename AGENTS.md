# Agents

## Stack and scope

Go web app driving the `m4b-merge` CLI. For settings, security, or migration changes, read README "Configuration", "Security", and "Migrating from the Python (Django) app", plus `docs/migration-from-python.md`.

## Audio changes

Read `internal/audio/processor.go` and `internal/audio/writecheck.go` before audio lifecycle changes.

- Keep one consistent settings snapshot per running job; settings changes apply to subsequent runs.
- Cancellation must terminate the subprocess group, not leave descendant writers running.
- Preserve job-scoped output ownership and a recoverable artifact if final relocation fails; never discover outputs by scanning for the newest shared file.
- Check writeability with a real temporary-file probe and report the offending setting, not just permission bits.
