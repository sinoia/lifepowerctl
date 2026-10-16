# Development practices

LifePower Control is developed for small Raspberry Pi systems, including
2 GB development machines and older SD-card deployment targets. Keep CPU,
memory, process, dependency, write-amplification, and storage costs modest.

Read `docs/project-state.md`, `docs/roadmap.md`, and the relevant ADR/reference
documents before changing behavior. Public issues and pull requests record
active work; private deployment facts must never be added to the repository.

## Resource discipline

- Run only one Go build, test, vet, or analysis command at a time.
- Prefer targeted tests while iterating and the sequential Makefile targets
  for normal verification.
- Do not add a dependency without considering install size, cold-build cost,
  memory, CPU, storage writes, and ongoing maintenance.
- Keep JavaScript small and dependency-free. The project has no Node,
  frontend-package-manager, bundler, or framework requirement.
- Run race tests, fuzzing, vulnerability scans, multi-architecture builds, and
  broad release matrices on suitable CI runners, not a constrained Pi.

Prepare and retain local caches:

```console
mkdir -p .cache/go-build .cache/go-mod build
make format
make test
make vet
make build
```

`make check` performs formatting verification, tests, vet, and build in that
order. Never run it alongside another Go command. A cold Go compile can be
silent for more than 30 seconds; do not start a second compiler.

## Tests and hardware

The ordinary test suite must be deterministic and hardware-independent.
Attached hardware must never cause `go test ./...` to access it automatically.
Use fakes for failures, corruption, external mutation, crash points, register
changes, wake actions, and power requests.

Real-board work is separately enabled and begins read-only. Access to a board
does not authorize register writes, `CFG_WRITE`, wake changes, shutdown,
reboot, watchdog/power-latch exercise, vendor-daemon disruption, or
fault-injection installation. Each disruptive procedure needs explicit owner
approval, a known recoverable target, captured starting state, recovery steps,
and final-state verification.

## Repository discipline

- Inspect `git status --short` before and after work.
- Preserve unrelated and user-authored changes.
- Never commit authentication data, controller state, job output, credentials,
  private keys, private infrastructure details, or copied vendor material.
- Avoid destructive Git cleanup or reset operations without explicit approval.
- Run `gofmt -w` only on changed Go files.
- Before committing, run `git diff --check`, inspect the final diff, and verify
  that only intended files are staged.
- Update owner docs, tests, project state, and ADRs when their contracts change.
