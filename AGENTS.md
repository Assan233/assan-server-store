# Repository instructions

## Scope and workflow
- These rules apply repository-wide; a nested `AGENTS.md` adds rules for its subtree.
- This is a personal Go backend deployed to NAS with Docker. Keep the baseline small; add infrastructure only for an actual feature.
- Before editing, inspect the working tree and relevant code. Preserve unrelated changes.
- Use a feature branch based on `main` and Conventional Commits (`feat:`, `fix:`, `docs:`). Commit and push only when explicitly requested.

## Architecture
- `cmd/server` owns process startup, dependency wiring and shutdown. Business behavior belongs in `internal/<feature>`.
- Keep HTTP transport in `internal/httpapi`; configuration loading and validation in `internal/config`.
- Prefer the standard library. When adding a dependency, explain its purpose, run `go mod tidy`, and commit the resulting module files.
- Introduce interfaces at consuming boundaries when needed; keep constructors explicit and state instance-owned.
- Use Go doc comments for exported declarations, `gofmt` for formatting, wrapped errors with `%w`, and `context.Context` for cancellable work.

## Task-specific references
- For API changes, read `internal/httpapi/AGENTS.md` and `docs/api.md` before editing routes.
- For container, configuration or runtime changes, read the deployment and configuration sections of `README.md`; update them alongside behavior changes.
- The sibling frontend is `../assan-fe-store`. Inspect its consumers before changing an API contract; edit that repository only when the task includes it.

## Verification and completion
- Run `make check` and `make build` for Go changes. Add behavioral tests for new behavior and regressions, including invalid inputs.
- For container changes, run `docker compose config --quiet` and `docker compose build`; verify health and shutdown when Docker is available.
- Keep credentials in environment variables or external secrets, and logs free of credentials and request bodies.
- Update the relevant documentation for configuration/API changes. Record breaking changes and migration steps in `docs/api.md` or `README.md`.
- Report changed behavior, commands run and their outcomes, and any checks blocked by the environment.
