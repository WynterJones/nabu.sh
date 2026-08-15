# Nabu.sh — Product Requirements Document

| Field | Value |
| --- | --- |
| Product | Nabu.sh |
| PRD version | 2.0 |
| Product stage | Feature-complete alpha; release hardening |
| Updated | August 15, 2026 |
| Platforms | macOS first, Linux supported |
| Canonical repository | https://github.com/WynterJones/nabu.sh |
| Primary AI runtime | Authenticated local Codex CLI |

## 1. Product definition

Nabu is a local, always-on AI operator that turns a mission into durable, verified work.

The user installs Nabu on a computer, creates one or more isolated workspaces, gives each workspace a mission and enough context to act, and then collaborates with one persistent operator through Chat. Nabu plans work, executes bounded tasks with Codex, uses approved tools, stores structured knowledge, schedules future checks, and surfaces results without requiring the user to manage agent sessions.

Nabu owns the operating system around the AI:

- durable mission and workspace context
- a dependency-aware task queue
- schedules and deterministic scripts
- approved secrets and MCP connectors
- supervised Codex runs
- datasets, files, apps, reports, and artifacts
- policy and approval enforcement
- recovery, backups, and health state
- a polished local web interface

Codex supplies reasoning and execution. Nabu supplies continuity, organization, policy, and evidence.

The product should feel like:

> Give one capable operator a mission, the context and tools to succeed, and a clear place to see what it did.

## 2. Core promise

Nabu must answer these questions immediately:

1. What is the mission for this workspace?
2. What is Nabu doing now?
3. What is planned next, and why?
4. What has Nabu produced?
5. What genuinely needs the user?

Every product surface must support one or more of these questions.

## 3. Product principles

### 3.1 One operator, multiple isolated workspaces

The user interacts with one personality: Nabu.

Each workspace is an independent business or project scope with its own:

- mission
- context readiness
- Chat history and threads
- tasks and dependencies
- calendar and schedules
- datasets
- apps and outputs
- reports
- memory and Soul
- secrets, scripts, MCP connectors, and policy

Switching workspaces must never leak state, secrets, records, or context between scopes.

Codex runs are internal workers. The UI must not invent a fake organization of named agents.

### 3.2 Chat is the front door; durable state is the operating system

Chat is the default page and the primary way to plan, steer, explain, and resolve work.

A useful conversation should promote important information into durable state when appropriate:

- mission or context updates
- tasks, dependencies, and planned dates
- schedules and plans
- policies and approvals
- memory and Soul proposals
- scripts, secrets, or MCP capability requests
- datasets and structured records
- reports and artifacts

Chat history is not the sole source of truth.

### 3.3 Proactive, not reckless

Nabu should gather missing context, propose useful work, and take safe reversible action without requiring continual hand-holding.

Nabu should automatically:

- read approved workspace files
- inspect approved repositories
- research public information
- use read-only MCP tools
- run tests and verification
- edit files inside approved workspaces
- create branches and local commits
- create tasks, plans, datasets, reports, and drafts
- run approved read-only scripts
- organize outputs into the workspace layout

Nabu should ask before actions that are externally consequential or difficult to reverse:

- production deployment
- public publishing or messaging
- spending or billing changes
- production data mutation
- authentication and security changes
- protected-branch merge
- destructive filesystem or workspace deletion

Approvals must protect real boundaries, not ordinary progress.

### 3.4 Evidence over claims

Nabu must not claim work is complete because a model said so.

Evidence may include:

- command exit status
- tests, builds, lint, or type checks
- changed files or Git diff
- structured dataset write results
- HTTP or API checks
- MCP tool results
- browser observations and screenshots
- generated artifacts
- a running local app health check
- report links

If only part of the requested work is verified, preserve the work and describe the remaining step accurately. Use “Needs You” or “Needs another step” rather than a misleading destructive-looking failure state when appropriate.

### 3.5 Mostly sleeping

Nabu is always available, not always invoking AI.

The daemon owns timers, health checks, leases, retries, queue state, schedules, and recovery. Codex is invoked only for reasoning or execution.

When no runnable work exists, an idle steward may re-evaluate the workspace at a conservative interval, normally no more than once every 15 minutes. It may create one to three useful tasks only when evidence supports them. It may also decide that nothing should happen until a future condition or date.

### 3.6 Keep infrastructure boring

Prefer:

- Go
- SQLite
- React and TypeScript
- Tailwind CSS and shadcn/Radix primitives
- filesystem and Git
- Codex CLI
- MCP
- HTTP and Server-Sent Events
- Markdown and typed JSON

Avoid cloud infrastructure that is not required for a local operator.

## 4. Target users and use cases

Nabu is intended for an owner, developer, researcher, or small team that wants a persistent local operator rather than a disposable chat session.

Representative missions include:

- grow and operate a collection of web products
- maintain and improve a software portfolio
- research markets, competitors, products, or technical options
- monitor analytics, errors, support signals, or infrastructure
- build and maintain datasets and research libraries
- create documents, reports, media, and local applications
- plan a multi-week initiative and execute its next safe steps
- maintain Nabu itself from a separate source checkout

The architecture must remain mission-agnostic.

## 5. Workspace model

### 5.1 Workspace lifecycle

A user can create an organized workspace or connect an existing folder.

Every newly created workspace receives its own onboarding conversation. A new workspace is not treated as ready merely because another workspace completed setup.

During onboarding Nabu should establish:

- whether the user is starting fresh or connecting an existing operation
- the desired outcome and timeframe
- current assets, repositories, sites, documents, and datasets
- target users or customers
- current baseline and measurable success signals
- available services, accounts, APIs, and MCP connectors
- approval, legal, financial, security, and publishing constraints
- what Nabu can safely begin without further input

Operational pages remain gated until context is ready. Chat and Settings remain available so setup can be completed.

Nabu should consolidate questions rather than asking one small question at a time. When it believes context is sufficient, Chat should present a clear confirmation action.

### 5.2 Organized workspace layout

Nabu-created workspaces use:

```text
workspace/
├── inbox/
├── documents/
├── media/
├── research/
├── data/
├── repos/
├── reports/
├── deliverables/
└── archive/
```

Placement rules:

- applications and source repositories belong in `repos/<project>/`
- durable documents belong in `documents/`
- research source material belongs in `research/`
- imported or generated data files belong in `data/`
- reports belong in `reports/`
- final handoff material belongs in `deliverables/`
- disposable incoming material may begin in `inbox/`

Nabu must not scatter application source across the workspace root.

Connecting an existing folder must not reorganize or overwrite it automatically.

### 5.3 Workspace deletion

The user must be able to remove a workspace from Nabu.

Default deletion removes Nabu’s scoped records and leaves the filesystem intact. Deleting filesystem contents is a separate destructive action requiring explicit confirmation that names the exact path. The active or only workspace cannot be removed without selecting or creating a safe replacement.

## 6. Product surface

The primary product areas are:

1. Chat
2. Tasks
3. Calendar
4. Database
5. Apps
6. Outputs
7. Reports
8. Settings

There is no separate “advanced mode.” Complexity is revealed only where it helps the user complete work.

### 6.1 Application shell

Desktop uses a compact floating navigation rail rather than reserving a full sidebar column. The page canvas may use the full window width.

The shell contains:

- Nabu wordmark
- current page title
- Codex model and reasoning badges
- Nabu mascot and compact status indicator
- Pause or Resume
- workspace switcher
- direct Chat action
- primary navigation flyout
- Settings flyout

Navigation cues may show:

- Chat activity or unread lifecycle messages
- Needs You task count
- current date beside Calendar
- dataset count
- unread report count

Hover must never leave a false active state. Flyouts must remain reachable while the pointer moves from their trigger into the menu.

Mobile uses a conventional full-screen drawer and touch-safe controls.

### 6.2 Chat

Chat is the default route.

Each workspace has one durable primary conversation. It shows the newest messages first as a bounded page and can load older messages without passing the full history into every Codex run.

Requirements:

- durable FIFO user-message queue
- Chat execution lane independent from task workers
- Chat can respond while tasks run
- streamed Markdown with headings, lists, tables, code, links, and redacted-secret badges
- automatic scroll when a user sends a message or Nabu begins thinking, unless the user intentionally scrolled away
- Slack-style threads attached to assistant messages
- thread panel pushes desktop layout instead of blocking it with an overlay
- user messages do not need avatars or thread actions
- assistant lifecycle messages may arrive when tasks start, complete, fail, or need approval
- messages and their threads can be deleted with confirmation
- important references render as compact task, report, dataset, app, file, approval, or secret-setup cards
- action cards support buttons, choices, approvals, and protected secret entry

Chat accepts stream-of-thought input. Nabu should organize mixed ideas into context, memory, datasets, plans, schedules, or tasks rather than blindly converting every sentence into immediate work.

### 6.3 Tasks

Tasks are durable units of executable work.

States:

```text
Idea
Ready
Running
Waiting
Needs Approval
Completed
Failed
Cancelled
```

Each task contains:

- title
- purpose
- mission connection
- definition of done
- priority: High, Normal, or Low
- workspace
- creator
- dependencies
- optional planned time
- runs, evidence, artifacts, and reports
- timestamps and terminal state

Task list sections are Doing, Needs You, Ready, and Finished. Finished is ordered most-recent-first, initially shows five, and loads additional items in bounded pages.

Task detail must provide:

- prominent title and purpose
- live definition-of-done state when evidence is available
- human-readable activity
- structured result and verification
- files, artifacts, datasets, reports, and app references
- formatted raw output for debugging
- Run Now for an eligible task
- retry or continue-with-Nabu recovery
- close/cancel, user-complete, and delete actions where valid

Run Now must explain unmet active prerequisites. A cancelled prerequisite does not silently trap the task forever; the user may explicitly override it when the remaining work is still valid.

### 6.4 Calendar

Calendar presents Nabu as a worker with history and future commitments.

It shows:

- completed work in the past
- currently running work
- genuinely time-bound planned tasks
- recurring and one-time schedules
- milestones and check-ins

The upcoming list shows only actionable future items. Status badges are unnecessary when the section already communicates the state.

Nabu normally plans detailed executable work for the next two weeks. Immediate foundation work should not be delayed merely to distribute activity across dates. Future scheduling is for real time dependencies, measurement windows, follow-ups, publishing dates, or recurring checks.

### 6.5 Database

Database provides workspace-scoped typed datasets for research and operational records.

Nabu should choose a dataset when information is naturally a collection, including:

- market and competitor research
- product or resource directories
- lead and opportunity lists
- sitemap or page inventories
- analytics snapshots
- findings and remediation catalogues
- experiment results

Requirements:

- dataset create, update, archive, restore, and delete
- typed schemas with additive evolution
- row insert, update, upsert, delete, search, filter, and sort
- cursor pagination
- JSON import and JSON/CSV export
- virtualized, resizable table presentation
- one-line truncated cells with a detail drawer
- URLs open safely in a new tab
- atomic dataset-plus-rows creation
- automatic bounded chunking for large model-produced writes
- no completion claim until the intended rows are actually persisted

The UI and runner must not surface an implementation-size limit as an unrecoverable user task failure. Nabu should split valid writes into safe batches.

### 6.6 Apps

Apps surfaces runnable local sites and applications that Nabu creates or connects.

Each app registration includes:

- name
- workspace-relative directory under `repos/`
- start command
- port and health path
- status
- optional auto-start setting
- bounded logs

The user can start, stop, restart, open, inspect logs, and navigate to the source folder. Nabu may register an app after creating it, but may not register the workspace root as an application directory.

### 6.7 Outputs

Outputs provides a human-friendly view of what Nabu made, regardless of whether the user browses the filesystem.

It includes:

- files and documents
- media
- artifacts
- repository links
- local applications

Any valid workspace path shown in Chat, Tasks, Runs, Outputs, or Reports should open in a consistent drawer.

The viewer supports:

- syntax-highlighted text with editing and save
- Markdown preview
- JSON formatting
- images
- video and audio
- PDF
- download
- folder listing and navigation when the target is a directory

### 6.8 Reports

Reports are durable, meaningful results rather than raw model messages.

Reports support Unread, Read, and Archived states, related tasks, artifacts, deletion, and workspace isolation. Markdown tables must remain responsive and truncate or wrap pathological identifiers without breaking page layout. JSON report bodies should render as structured JSON where possible.

Nabu creates a report only when the result is worth returning to.

### 6.9 Settings

Settings groups local operator configuration separately from workspace capabilities.

General:

- Operator: discovered Codex model, reasoning effort, and maximum parallel tasks
- Workspaces: create, connect, switch, icon, update, and delete
- Remote access: Tailscale Serve status and guided setup

Workspace:

- Policy
- Schedules
- Scripts
- Memory
- Soul
- Secrets
- MCP connectors

## 7. Planning and execution

### 7.1 Task creation

Tasks may come from:

- the user
- Chat
- onboarding or orientation
- a plan
- a schedule
- a script result
- a completed or failed task
- the idle steward

Nabu should prefer a small coherent queue over speculative backlog generation.

Dependencies are reserved for real prerequisites. Independent tasks should run independently.

### 7.2 Concurrency

The user configures one to eight parallel AI task workers. Default is one; two is recommended when Codex capacity allows it.

Requirements:

- a global concurrency limit across workspaces
- atomic claims prevent duplicate execution
- dependencies must be satisfied before automatic claim
- Chat uses a separate FIFO lane and has priority over starting new autonomous work
- orientation is serialized and waits for a safe view of active work
- Pause and Stop cancel exact active runs without corrupting unrelated tasks
- status UI lists all active tasks and Chat work

### 7.3 Planning horizon

Nabu reasons across a two-week rolling horizon.

- Do now: unblocked work that advances the mission
- Plan soon: work dependent on current tasks or near-term evidence
- Schedule: work that genuinely depends on a future time or recurrence
- Milestone: a measurable outcome or review point

Nabu must not manufacture idle gaps by scheduling foundational work days into the future when it can safely proceed now.

### 7.4 Result contract

Every Codex run returns a normalized bounded result containing:

- terminal status
- concise summary
- definition-of-done verification
- evidence
- changed files and artifacts
- optional report
- optional dataset writes
- optional local app registrations
- attention reason and remaining work when incomplete

Malformed structured output receives one repair attempt before the run becomes actionable failure. Nabu must never store an empty or unknown terminal status.

## 8. Capabilities and integrations

### 8.1 Codex

Codex CLI is the default and initial AI provider.

Nabu discovers installed Codex models and supported reasoning levels rather than hard-coding an outdated list. The selected model, reasoning level, approved workspace, relevant memory, task packet, and available capabilities are explicit for every run.

Provider abstraction may be added later, but Claude Code or another provider is not required for the first public release.

### 8.2 Browser and public web

Browser capability is supplied to Codex through a standard browser MCP server when interactive UI work is required. Nabu does not need to build a proprietary browser.

Requirements:

- browser MCP availability is discoverable before the run
- task packets state the available browser capability accurately
- UI and UX tasks use browser evidence when configured
- authenticated browser work uses the connector’s supported OAuth or user session
- the user can complete OAuth in a new tab and Nabu polls the persisted auth state
- a completed OAuth callback must update Settings without requiring a restart
- missing browser capability blocks only work that truly requires interactive verification
- public research may use non-browser web or HTTP tools when sufficient
- browser screenshots and observations become artifacts

Codex must not repeatedly attempt sandbox-blocked local Chromium when a browser MCP is the configured path.

### 8.3 Secrets

Secrets are generic workspace capabilities, not provider-specific integrations.

Secret values:

- are entered only through protected UI
- live in the operating-system credential store
- are write-only after save
- never enter Chat, SQLite, prompts, logs, reports, or command arguments
- may be bound to script environment variables or MCP authentication

Metadata such as name, label, description, and bindings may live in SQLite.

### 8.4 Scripts

Scripts provide repeatable deterministic capabilities and may use bound secrets as environment variables.

Nabu can create and edit a script inside the managed scripts directory, register it, test it, and use it manually, on a schedule, or as task context. Script execution remains bounded by workspace, timeout, output, and access policy.

Provider-specific integration manifests are optional convenience adapters. They must not be required when a generic secret plus an approved script or MCP server can accomplish the work.

### 8.5 MCP connectors

Nabu supports local and remote MCP servers with:

- enabled and required state
- read-only or broader tool access policy
- local command or remote HTTPS transport
- secret bindings
- OAuth authentication where offered by the server
- auth-status polling
- tool inventory on new Chat, task, and orientation runs

Unknown or write-capable tools fail closed according to policy. Read-only tools may run automatically.

## 9. Memory and personality

### 9.1 MEMORY.md

Memory contains concise durable facts and decisions that should influence future work:

- business and product context
- terminology
- stable preferences
- constraints
- lessons
- important tool and account metadata that is not secret

Memory changes from Chat are proposed or applied through typed effects. The default Settings view is Preview, with an explicit Edit mode.

### 9.2 Daily memory

Operational notes live in `memory/YYYY-MM-DD.md` and remain concise.

### 9.3 SOUL.md

Soul defines Nabu’s evolving character, working style, values, and self-reflection without overriding policy or inventing authority.

Soul may improve gradually from observed collaboration preferences. It cannot weaken security, approvals, workspace boundaries, or evidence requirements.

## 10. Policy and approvals

Action categories:

```text
Read
Work
Publish
Dangerous
```

Default behavior:

- Read: allow
- Work inside approved workspace: allow
- Publish: ask
- Dangerous: always ask

Approvals are durable, scoped to an exact action, and have Pending, Approved, Rejected, or Expired state. Approving an action must revalidate the exact target before execution. Model text cannot bypass daemon enforcement.

## 11. Persistence and recovery

SQLite is authoritative for structured operational state.

Core records include:

- settings and workspace scopes
- missions and policy
- tasks and dependencies
- runs and events
- messages and threads
- approvals
- reports and artifacts
- schedules, scripts, and script runs
- memory proposals
- secrets metadata
- MCP connectors
- datasets and rows
- local app registrations

Large human-readable files and run streams live on disk, with metadata in SQLite.

Requirements:

- additive versioned migrations
- foreign-key integrity
- transactional multi-record operations
- restart recovery for running tasks, Chat, scripts, and schedule claims
- daily verified backups
- safe restore procedure
- log rotation
- disk-space health checks
- bounded output capture
- no infinite retry loops
- user data preserved during ordinary uninstall

## 12. Service, API, and remote access

`nabud` runs as a user LaunchAgent on macOS or user systemd service on Linux.

Default binding:

```text
127.0.0.1:7777
```

The React frontend is embedded in the Go binary for production.

The local API includes groups for:

- status, setup, mission, pause, and health
- scopes and workspace icons
- Chat, threads, and events
- tasks, runs, and recovery
- calendar and schedules
- datasets and rows
- apps and logs
- outputs and files
- reports, approvals, and policy
- scripts, memory, and Soul
- secrets and MCP connectors
- remote access and service control

Server-Sent Events update the UI without requiring a refresh. Events must be workspace-scoped and idempotent at consumers.

Tailscale Serve is the supported remote-access path. Nabu provides UI-guided setup, status, HTTPS URL, teardown, and actionable certificate or routing errors. Nabu does not bind directly to a public network interface by default.

## 13. Security requirements

- bind to localhost by default
- validate Host and Origin for local and Tailscale requests
- treat workspace selection as an authorization boundary
- use exact approved working directories
- keep Codex in an explicit workspace-write sandbox
- never silently broaden filesystem scope
- keep credentials out of model-visible and persisted text
- prevent unbounded redirects, private-network SSRF, and proxy inheritance for generated HTTP adapters
- bound request, response, log, artifact, and dataset sizes
- validate all model-produced effects against authoritative state
- require exact-target approval for destructive operations
- preserve evidence of failures without preserving secret payloads
- support a documented responsible-disclosure process before public beta

## 14. Design requirements

Nabu uses a professional neutral dark interface:

- near-black graphite base, not green-tinted surfaces
- supplied owl background with a restrained dark overlay
- teal/green only as the brand accent
- amber for setup, approval, and attention
- red only for destructive actions and true failures
- glassy surfaces used sparingly
- subtle borders and depth rather than heavy glow
- consistent button heights and three button levels: primary, secondary, ghost
- strong hover, active, focus-visible, disabled, and loading states
- responsive behavior at 360, 768, and 1280 pixel widths
- no horizontal page overflow
- accessible labels, keyboard interaction, and reduced-motion support
- native scroll behavior with visible scrollbars on actual scroll owners

Brand assets:

- full Nabu wordmark in the header
- cropped owl for favicon, status, and assistant avatar
- status mascot variants for Idle, Active, Awaiting Approval, Asking, Failed, and Success
- startup introduction may show Nabu, version, and “Made by Wynter.ai,” but must be brief and skippable after first view

## 15. Installation and distribution

Development requirements:

- Go 1.24+
- Node.js 22+
- authenticated Codex CLI
- Git

Public releases must not require end users to build from source.

Release distribution requires:

- versioned macOS and Linux binaries
- embedded frontend assets
- SHA-256 checksums
- signed release artifacts where practical
- a Homebrew installation path
- upgrade, rollback, and uninstall commands
- migration and backup verification before upgrade
- a diagnostic `nabu doctor`

The source installer remains a development fallback.

## 16. Open-source requirements

Canonical repository:

```text
https://github.com/WynterJones/nabu.sh
```

Before declaring Nabu open source, the repository requires:

- [ ] an OSI-approved code license; Apache-2.0 is recommended
- [ ] explicit terms for Nabu trademarks and supplied brand artwork
- [ ] `CONTRIBUTING.md`
- [ ] `SECURITY.md`
- [ ] `CODE_OF_CONDUCT.md`
- [ ] `CHANGELOG.md`
- [ ] issue and pull-request templates
- [ ] GitHub Actions for Go race tests, vet, frontend tests, lint, typecheck, build, and dependency audit
- [ ] dependency update automation
- [ ] release artifact generation and checksums
- [ ] architecture and threat-model documentation
- [ ] installation, upgrade, backup, restore, and troubleshooting documentation

Public source without a license is not yet an open-source release.

## 17. Self-development requirements

Nabu may work on its own source when the repository is an approved workspace repository, for example `repos/nabu/`.

Safe self-development separates:

1. the stable installed Nabu coordinating the work
2. a candidate source checkout and isolated candidate runtime

For a self-improvement task Nabu should:

1. create a task branch
2. inspect and modify the candidate source
3. run complete backend and frontend verification
4. build a candidate binary and embedded frontend
5. launch it on a different port with an isolated Nabu home
6. exercise relevant API and browser acceptance tests
7. produce a change and risk report
8. request approval before merge, release, migration of real data, or stable-service replacement

The running process must never overwrite itself. Promotion requires an external updater or supervisor with atomic replacement, health check, and rollback.

## 18. Delivery status

The original ten implementation phases have been delivered into the alpha codebase. “Implemented” does not mean “public-release verified.”

| Phase | Capability | Status |
| --- | --- | --- |
| 1 | Go daemon, CLI, embedded React shell, SQLite | Implemented |
| 2 | Setup, missions, organized workspace scopes | Implemented |
| 3 | Supervised Codex runner and streamed activity | Implemented |
| 4 | Durable task queue, evidence, recovery | Implemented |
| 5 | Orientation and proactive queue management | Implemented |
| 6 | Durable Chat, queue, Markdown, threads, effects | Implemented |
| 7 | Policy, approvals, exact-target enforcement | Implemented |
| 8 | Reports, memory, Soul, artifacts | Implemented |
| 9 | Schedules, scripts, secrets, automation | Implemented |
| 10 | Pause, recovery, backups, health, rate limits | Implemented |
| 11 | Datasets, Apps, Outputs, MCP, parallel tasks | Implemented; hardening |
| 12 | Open-source release engineering | In progress |
| 13 | Safe self-development and candidate promotion | Planned |

## 19. Public alpha exit criteria

Nabu is ready for `v0.1.0-alpha` when all of the following pass from a clean supported machine:

- [ ] repository has a code license and community/security files
- [ ] CI passes from a fresh clone
- [ ] a release binary installs without Go or Node
- [ ] Codex detection and authentication checks are actionable
- [ ] a workspace can be created, onboarded, switched, and removed safely
- [ ] Chat can gather context and confirm readiness
- [ ] a task can be created, run, verified, recovered, and deleted
- [ ] two independent tasks can run in parallel when configured
- [ ] dependencies prevent invalid automatic ordering
- [ ] Chat remains responsive while tasks run
- [ ] browser MCP authentication persists and tools appear in a new run
- [ ] secrets can be bound to a script or MCP server without entering model-visible state
- [ ] a large dataset write is automatically batched and fully persisted
- [ ] a created app appears in Apps and can start, open, stop, and show logs
- [ ] output files and folders open correctly in the shared viewer
- [ ] reports support unread, read, archive, restore, and delete
- [ ] daemon restart recovers interrupted durable state
- [ ] computer restart preserves workspace state and resumes correctly
- [ ] backup, migration, restore, upgrade, rollback, and uninstall are tested
- [ ] 72-hour unattended soak test has no duplicate execution, runaway retries, state corruption, or secret leakage
- [ ] macOS and Linux installation paths are documented

## 20. Product-complete experience

Nabu is product-complete when a user can:

1. install a signed release
2. authenticate an existing Codex CLI
3. create or connect multiple isolated workspaces
4. complete context setup conversationally
5. give each workspace a durable mission
6. connect generic secrets, scripts, and MCP tools as needed
7. discuss and approve a coherent two-week plan
8. let Nabu execute independent work in parallel
9. see exactly what is running and why
10. receive useful Chat updates without polling task pages
11. inspect evidence, files, apps, datasets, and reports without opening the workspace folder
12. resolve genuine blockers through contextual cards and approvals
13. close the browser without stopping work
14. restart the computer without losing state
15. remain idle without wasting AI usage
16. recover from Codex unavailability and rate limits
17. upgrade or roll back without losing workspaces or secrets
18. understand the security model and report vulnerabilities
19. contribute to the project through a documented open-source workflow
20. optionally let a stable Nabu prepare and verify improvements to a separate Nabu candidate checkout

## 21. Explicit non-goals for the first public release

- hosted multi-tenant Nabu cloud
- mandatory Nabu account or hosted authentication
- native mobile application
- a fictional multi-agent company UI
- unrestricted autonomous publishing, spending, or production mutation
- storing secrets in Nabu’s database
- building a proprietary browser instead of using standard MCP capability
- supporting every AI provider before the Codex path is reliable
- replacing GitHub, an IDE, or full database administration tools

## 22. Final product shape

```text
                              NABU

                    One operator, on a mission

                               │
                               ▼

                     Local React web interface

      Chat · Tasks · Calendar · Database · Apps · Outputs · Reports

                               │
                  ┌────────────┴────────────┐
                  ▼                         ▼
          Durable local state       Approved capabilities
        SQLite · files · Git      Codex · MCP · scripts · secrets

                  └────────────┬────────────┘
                               ▼

               Verified work inside isolated workspaces
```
