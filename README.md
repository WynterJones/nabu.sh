# Nabu.sh

Nabu is a local, always-on AI operator powered by your existing Codex CLI. It owns durable missions, workspace-scoped queues, approvals, reports, schedules, memory, and one steering conversation while Codex handles bounded reasoning and execution.

## Local development

Requirements:

- Go 1.24+
- Node.js 22+
- Codex CLI installed, authenticated, and available on `PATH`
- Git

Build and run the production-shaped application:

```bash
make frontend
make build
NABU_HOME="$PWD/.nabu-dev" ./bin/nabud
```

Open [http://127.0.0.1:7777](http://127.0.0.1:7777). Closing the browser does not stop the daemon.

Frontend development with hot reload:

```bash
cd frontend
npm install
npm run dev
```

The Vite server proxies `/api` to the Go daemon.

## Product areas

- Overview: live operator state, queue, approvals, and recent results
- Chat: one durable conversation per workspace, a restart-safe FIFO message queue, newest-10 paging, Slack-style threads, planning, and safe durable effects
- Tasks: Codex-assisted drafting, scheduling, lifecycle, failure evidence, retry, deletion, and cancellation
- Calendar: completed work, current work, planned tasks, and recurring schedules in one timeline
- Database: workspace-scoped typed datasets with row CRUD, server search/filter/sort, JSON import, and CSV/JSON export
- Reports: durable Markdown results and linked artifacts
- Settings: workspaces and icons, policy, integrations and Keychain-backed credentials, schedules, scripts, memory, health, and discovered Codex model/reasoning controls

Each Nabu-created workspace is organized with `inbox`, `documents`, `media`, `research`, `data`, `repos`, `reports`, `deliverables`, and `archive`. Connecting an established folder leaves its structure untouched.

## Verification

```bash
go test -race ./...
go vet ./...

cd frontend
npm run typecheck
npm run lint
npm test
npm run build
npm audit --omit=dev
```

Operational recovery, backups, logs, service commands, and uninstall behavior are documented in [OPERATIONS.md](./OPERATIONS.md).

## Safety model

Nabu listens only on localhost, validates Host and Origin headers, runs Codex with an explicit workspace-write sandbox, keeps high-impact actions behind durable approvals, bounds captured output, and stores operational state in SQLite. Workspace selection is an authorization boundary: tasks and product records are scoped to the active approved workspace. Integration secrets belong in Settings and the operating system credential store—never in Chat, SQLite, prompts, logs, or command arguments.
