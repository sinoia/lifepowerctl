# Contributing

Contributions are welcome when they preserve the project's conservative
hardware and resource boundaries. Start with [Project state](docs/project-state.md),
[Architecture](docs/architecture.md), [Development](docs/development.md), and
[AGENTS.md](AGENTS.md).

For a change:

1. Open or identify an issue when behavior, compatibility, safety, or scope
   needs discussion.
2. Keep the patch focused and avoid unrelated formatting or dependencies.
3. Add deterministic tests; ordinary tests must never access attached hardware.
4. Run `make format`, then `make check` sequentially.
5. Update owner documentation, security/safety analysis, and an ADR when the
   contract changes.
6. Explain recovery, resource, compatibility, and hardware implications in
   the pull request.

Do not submit credentials, real controller state, job output, private host or
network details, copied manufacturer documents, build artifacts, or test logs
containing personal data.

Any real register write, wake programming, reboot, shutdown, power-loss test,
vendor-service disruption, or fault-injection deployment requires a separately
reviewed procedure and explicit authorization from the hardware owner. A pull
request or access to a test host is not that authorization.

By contributing, you agree that your contribution is provided under this
repository's MIT License and that you have the right to submit it.
