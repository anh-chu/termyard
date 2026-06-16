# tmux-spawn scope reaper plan

> Status: Deferred. Leak is real but slow (about one orphan per day on this host) and a blunt reaper risks killing intentionally detached processes (observed: a long-lived sshfs mount). Revisit if accumulation becomes a problem; if implemented, gate on an age threshold.

## Problem

On hosts where tmux is built with systemd support (tmux 3.5a here, `ldd /usr/bin/tmux` shows `libsystemd.so.0`), every new pane is placed in a transient systemd unit named `tmux-spawn-<uuid>.scope`. tmux creates the scope with `StartTransientUnit` but never calls `StopUnit` when the pane closes. A scope stays `active` as long as any process in its cgroup is alive.

When a pane child detaches from the foreground process group (started with `nohup`, backgrounded with `&` then `disown`, or calls `setsid()`), it survives pane close. The scope then lives forever, holding a leaked process. We observed 12 such orphans on this box: gh-roadmap dev servers, esbuild, repowire python daemons, agent-browser daemons, and maestro processes, several running for 11 to 13 days.

This is not a Termyard code defect. `pkg/tmux/client.go:391` runs commands in the foreground (`command; exec $SHELL -i`), and there is no `setsid`, `Setpgid`, `SysProcAttr`, `StartTransientUnit`, or `systemd-run` anywhere in the Go code. Scope creation is entirely inside tmux. But Termyard already manages the tmux server, so a reaper fits naturally as a quality-of-life feature.

## Goal

A background goroutine in the Termyard server that detects `tmux-spawn-*.scope` units whose owning pane is gone and stops them, so detached pane children do not accumulate as live scopes indefinitely.

## Mechanism

Run a ticker (default 60s). Each cycle:

1. Collect live pane pids: `tmux list-panes -a -F '#{pane_pid}'`.
2. List spawn scopes and resolve each scope's leader pid (cgroup leader or `systemctl --user show -p ExecMainPID`).
3. For any scope whose leader pid is not in the live pane set, run `systemctl --user stop <scope>`. With the default `KillMode=control-group`, this kills any survivors in the cgroup.

## Decisions to lock before coding

- **Match key.** Scope names carry a uuid, not a pid. Need a reliable pid to scope mapping. The scope leader pid recorded at spawn should equal the pane's `pane_pid` (both the pane shell). Verify this holds on a live pane before trusting it.
- **Grace window.** Require a pid to be absent for at least one full cycle before reaping, to avoid racing session teardown. Check interaction with the control-mode exit handler at `pkg/tmux/controlmode.go:261`.
- **Default behavior.** Reaper runs by default. A scope is only stopped when its owning pane is gone, so live work is never touched. Provide `TERMYARD_REAP_SCOPES=false` as an off switch and `TERMYARD_REAP_SCOPES=log` for a dry-run mode that reports what it would stop without acting.
- **systemd availability.** No-op when `systemctl --user` is unavailable (macOS launchd, non-systemd Linux). Detect once at startup and skip the goroutine entirely.

## Files

- New: `pkg/tmux/scope_reaper.go` (roughly 50 to 70 lines). Exposes `StartScopeReaper(ctx)`, with helpers `listLivePanePids`, `listSpawnScopes`, and `reapOrphans`.
- Wire into server startup alongside the other monitors in `pkg/commands/server/server.go`.
- Review the race against `pkg/tmux/controlmode.go:261`.

## Validation

- Spawn a pane, run `setsid sleep 600 &`, close the pane. The scope is orphaned and the reaper stops it within one cycle.
- Live panes are never reaped across several cycles.
- On a non-systemd host the reaper stays inert.
- `go test ./pkg/tmux/...` and `go build ./...` pass.

## Notes

Reaper is on by default. The grace window (one missed cycle before reaping) is the main safety guard against racing session teardown, so verify it against the control-mode exit handler before shipping.
