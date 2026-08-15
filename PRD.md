# Nabu.sh — Product Requirements Document

## Product

**Nabu** is a local, always-on AI operator with one mission.

The user installs Nabu on a Mac or Linux computer, opens a polished dark-mode web UI, gives Nabu a mission and access to selected workspaces, and lets it work continuously.

Nabu uses the user's existing **Codex CLI** installation as its only AI engine.

Nabu itself does not implement an LLM agent loop and does not require an OpenAI API integration. It supervises Codex processes, owns durable state, manages a task queue, enforces simple approval rules, stores reports and memory, and provides a clean web interface for steering the operator.

The product should feel like:

> Install an AI operator, give it a mission, and check in when it needs you.

---

# Core Promise

Nabu should answer four questions immediately:

1. **What is the mission?**
2. **What is Nabu doing right now?**
3. **What has Nabu accomplished?**
4. **What needs my approval or direction?**

Everything in the product should support those questions.

---

# Product Ethos

## One Mission

Nabu has one active mission.

Every task must support that mission.

Nabu should prefer finishing useful work over generating a large backlog.

---

## One Operator

The user interacts with one personality: **Nabu**.

Codex processes are internal workers.

The UI must not expose a fake organization of named AI agents.

---

## Queue Over Chat

Chat is a steering interface.

The task queue is the operating system.

A useful chat message should normally change durable state by:

- updating the mission
- adding context
- creating a task
- changing task priority
- cancelling work
- approving work
- changing a policy
- recording a durable decision

Chat should not become the only place important information exists.

---

## Durable State Outside Codex

Nabu must continue operating correctly when:

- Codex exits
- Codex crashes
- a run times out
- the browser UI is closed
- the computer restarts
- a task fails
- Codex usage is temporarily unavailable

Mission, tasks, reports, approvals, configuration, and history belong to Nabu.

---

## Evidence Over Claims

A Codex response is not enough to mark important work complete.

Where practical, tasks should have observable evidence such as:

- exit code
- test output
- build output
- changed files
- Git diff
- URL check
- generated artifact
- screenshot
- pull request URL
- script result
- structured report

---

## Safe but Not Annoying

Nabu should be allowed to research and prepare work without repeatedly interrupting the user.

Simple default:

### Allow Automatically

- read files
- inspect repositories
- use approved scripts
- search/read the web
- run tests
- run local commands
- edit files in approved workspaces
- create branches
- create local commits
- generate drafts
- create reports

### Ask First

- merge to protected branches
- deploy production
- publish public content
- send external messages
- modify production data
- delete important data
- spend money
- change billing
- change authentication/security settings

The user can change individual approval rules.

---

## 24/7 Means Mostly Sleeping

Nabu must not continuously call Codex just to prove it is alive.

The daemon should handle:

- timers
- schedules
- worker status
- queue state
- retries
- approvals
- process recovery

Codex should be invoked only when reasoning or execution is required.

---

## Keep Infrastructure Boring

Prefer:

- Go
- SQLite
- React
- TypeScript
- Tailwind CSS
- shadcn/ui
- filesystem
- Git
- Codex CLI
- HTTP
- Server-Sent Events
- Markdown

Avoid unnecessary distributed infrastructure.

---

# Primary Use Case

The initial working mission can be:

> Grow qualified traffic, product usage, and paid adoption across Wynter.ai.

Nabu should be capable of work such as:

- inspect website repositories
- review analytics exports
- review Search Console data
- research competitors
- find SEO opportunities
- inspect pages
- identify broken or weak pages
- prepare new pages
- improve copy
- fix code
- run tests
- prepare Git changes
- create reports
- monitor recurring metrics
- notice changes
- create follow-up tasks
- ask for approval before high-impact actions

The core architecture must remain mission-agnostic.

---

# Product Surface

Nabu has five primary areas:

1. **Overview**
2. **Tasks**
3. **Reports**
4. **Chat**
5. **Settings**

There is no separate Advanced mode.

Implementation details remain secondary.

---

# User Experience

## Install

Example installation:

```bash
curl -fsSL https://nabu.sh/install.sh | bash
nabu setup
```

Setup verifies:

- Codex CLI is installed
- Codex is authenticated
- Git is installed
- local workspace is writable
- Nabu service can be installed

Nabu then creates its local workspace, installs the background service, starts the local server, and opens the UI.

Default UI:

```text
http://127.0.0.1:7777
```

The browser is only the interface.

Closing the browser must not stop Nabu.

---

# First-Run Setup

The first-run experience should be short.

## Step 1 — Name

Default:

```text
Nabu
```

The user may change the display name.

---

## Step 2 — Mission

Prompt:

> What should Nabu be responsible for accomplishing?

Example:

```text
Grow qualified traffic, users, and paid adoption for Wynter.ai.
```

---

## Step 3 — Context

The user can provide a short description of:

- business
- products
- audience
- priorities
- important constraints

This becomes durable workspace context.

---

## Step 4 — Workspaces

The user selects folders or Git repositories Nabu may work in.

Example:

```text
~/Code/wynter-ai
~/Code/barnumpt
~/Code/account-wynter-ai
```

Nabu must never assume access outside approved workspaces.

---

## Step 5 — Autonomy

Show a short permissions screen.

Example:

```text
Research and read                 Allow
Edit approved workspaces          Allow
Run local scripts/tests           Allow
Create branches/commits           Allow
Create draft work                 Allow

Merge protected branches          Ask
Deploy production                 Ask
Publish publicly                  Ask
Send external messages            Ask
Delete important data             Ask
Spend money                       Ask
```

---

## Step 6 — Start

The final setup screen summarizes:

```text
Nabu is ready.

Mission:
Grow qualified traffic, users, and paid adoption for Wynter.ai.

Codex:
Connected

Workspaces:
3

Autonomy:
Research + local work allowed
External changes require approval
```

Primary action:

**Start Mission**

---

# Dark UI Direction

Nabu is dark mode only.

The interface should feel:

- calm
- technical
- polished
- minimal
- trustworthy
- high-density without being cluttered

Avoid:

- glowing sci-fi dashboards
- fake terminal decoration
- excessive gradients
- giant cards
- excessive charts
- excessive status badges
- fake agent avatars
- bright saturated backgrounds

Use:

- near-black background
- slightly lighter surfaces
- subtle borders
- muted text
- one restrained green/teal accent
- amber only for approvals/warnings
- red only for real failures/destructive actions
- compact typography
- clear hierarchy

---

# Frontend Architecture

## Stack

Use:

```text
React
TypeScript
Vite
Tailwind CSS
shadcn/ui
Radix primitives
Lucide icons
```

The frontend is compiled into static assets.

The Go application embeds the production frontend assets into the Nabu binary.

This avoids requiring a separate Node.js web server in production.

Development may run the Vite dev server separately.

---

# Vercel Chatbot Usage

Vercel's `vercel/chatbot` project should be treated as a **chat UX/component reference**, not as Nabu's application architecture.

Useful concepts/components to adopt or adapt:

- polished message list
- streaming assistant messages
- markdown rendering
- syntax-highlighted code blocks
- copy controls
- attachment presentation
- scroll-to-bottom behavior
- conversation loading states
- inline tool/activity presentation
- responsive chat composer
- keyboard behavior
- shadcn/Radix visual primitives

Do not carry over infrastructure Nabu does not need:

- AI Gateway
- direct model-provider APIs
- multi-model selector
- hosted authentication
- Neon/Postgres
- Vercel Blob
- cloud deployment requirements
- server-side model execution
- Vercel-specific persistence

All chat requests go:

```text
React UI
   ↓
nabud
   ↓
Codex CLI
```

Nabu owns the conversation state.

Codex provides reasoning.

---

# Main Layout

Desktop layout:

```text
┌─────────────────────────────────────────────────────────────┐
│ NABU                                      ● ACTIVE    Pause │
├──────────────┬──────────────────────────────────────────────┤
│              │                                              │
│ Overview     │                                              │
│ Tasks        │               Current Page                   │
│ Reports      │                                              │
│ Chat         │                                              │
│              │                                              │
│ Settings     │                                              │
│              │                                              │
├──────────────┴──────────────────────────────────────────────┤
│ Ask or steer Nabu...                                       │
└─────────────────────────────────────────────────────────────┘
```

The global composer may appear on Overview and optionally other operational screens.

Full Chat has its own larger composer.

---

# Overview

Overview is the default page.

It should answer the state of the mission without requiring navigation.

## Header

Show:

- Nabu
- current status
- Pause / Resume
- settings shortcut

Status should be one of:

```text
Working
Idle
Waiting for Approval
Paused
Needs Attention
```

Avoid exposing internal worker terminology here.

---

## Mission

Compact mission block:

```text
MISSION

Grow qualified traffic, product usage, and paid adoption
across Wynter.ai.
```

Allow quick edit.

Mission changes require confirmation because they can reprioritize the queue.

---

## Current Work

Show the most important active task.

Example:

```text
NOW

Researching Search Console opportunities

Nabu is comparing high-impression queries against
existing product pages.

Started 18m ago

[View Task]
```

If no task is running:

```text
Nabu is idle.
Next orientation in 42m.
```

---

## Queue

Show a maximum of five upcoming tasks.

Example:

```text
NEXT

1. Prepare landing page for funnel design review
2. Investigate VSLMachine signup drop
3. Repair structured data on /tools

[View All Tasks]
```

---

## Needs You

Only show when action is required.

Example:

```text
NEEDS YOU

Publish "AI Funnel Design Review"

Nabu completed the page, tests passed, and a preview is ready.

[Review] [Approve]
```

Approvals should be visually obvious without dominating the page.

---

## Latest Results

Show recent meaningful completed work.

Example:

```text
LATEST RESULTS

Search opportunity analysis
Found 14 underserved queries and prepared one page brief.

Site health
Fixed three broken internal links.

Conversion review
Identified a signup drop beginning after the latest release.
```

---

## Mission Score

Optional metrics configured by the user.

Keep this intentionally small.

Maximum default visible metrics: 4.

Example:

```text
Visitors     1,240   +18%
Signups         72    +9%
Trials          31   +11%
Sales            9    +3%
```

Metrics are not required for Nabu to function.

---

# Tasks

Tasks are the operational center of Nabu.

## Task States

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

`Idea` is intentionally separate from `Ready`.

Nabu can record potentially useful work without allowing the active queue to grow endlessly.

---

# Task List

Default grouping:

```text
Running
Needs You
Ready
Completed
```

Each task row shows only:

- title
- status
- priority
- age/duration
- workspace if relevant

---

# Task Detail

Task detail includes:

## Summary

```text
Title
Purpose
Related mission/objective
Status
Priority
Created by
```

---

## Why This Matters

One short explanation tying the task to the mission.

---

## Definition of Done

Checklist of required outcomes.

Example:

```text
- Page route exists
- Metadata is present
- Internal links added
- Build passes
- Screenshot generated
```

---

## Activity

Human-readable activity stream:

```text
23:02 Started Codex run
23:03 Read Search Console report
23:07 Inspected /tools pages
23:11 Modified landing page
23:14 Ran test suite
23:16 Generated preview
```

Do not dump every token or low-level event into the default activity list.

---

## Output

Show:

- summary
- files changed
- Git diff link/view
- reports
- screenshots
- URLs
- test results
- other artifacts

---

## Raw Output

A collapsed section can expose:

- Codex output
- stdout
- stderr
- command logs

This exists for debugging but is not the primary UX.

---

# Chat

Chat is for steering the operator.

It is not a separate generic chatbot.

Examples:

```text
Focus on VSLMachine conversion until we understand the signup drop.
```

```text
Why did you choose this task?
```

```text
Stop the current task.
```

```text
Add the BarnumPT repository to the current context.
```

```text
Don't publish SEO articles without asking me first.
```

```text
What did you accomplish today?
```

---

# Chat Behavior

Nabu should classify meaningful user messages into durable effects.

Possible effects:

```text
conversation_only
create_task
update_task
cancel_task
update_mission
update_context
update_policy
approve_action
reject_action
pause
resume
request_report
```

The response should state what changed.

Example:

```text
User:
Stop SEO work for now. Figure out why VSLMachine isn't converting.

Nabu:
I paused the two ready SEO tasks and changed the current focus to
VSLMachine conversion. I created an investigation task and moved
it to the top of the queue.
```

The task board should immediately reflect the change.

---

# Chat Sessions

Use one primary conversation with Nabu.

Do not create dozens of separate chat threads by default.

The user can inspect historic messages chronologically.

Important information is promoted into durable context instead of depending on chat history forever.

---

# Chat Rendering

Support:

- streamed text
- Markdown
- headings
- lists
- code blocks
- inline code
- file references
- task references
- report references
- approval cards
- activity cards
- diff previews
- image/screenshot previews

Chat should be powerful without becoming visually busy.

---

# Codex Integration

Codex CLI is Nabu's only AI engine.

Nabu must detect and verify Codex during setup.

Nabu must not require the user to enter an API key into Nabu.

The local Codex installation remains responsible for its own authentication.

---

# Codex Runner

`nabud` directly owns Codex processes.

No tmux requirement.

For each run, Nabu tracks:

```text
run_id
task_id
PID
working_directory
started_at
ended_at
status
exit_code
Codex session/thread identifier when available
stdout
stderr
structured events
result
```

Nabu should prefer Codex machine-readable output when available.

---

# Run Types

Nabu needs only three conceptual run types.

## Orient

Purpose:

> Given the mission and current state, decide what useful work should happen next.

An orientation run can:

- review completed work
- inspect current signals
- identify blockers
- create a small number of tasks
- reprioritize ready tasks
- decide that no new work is needed

Orientation must not automatically create a massive backlog.

Default maximum new ready tasks from one orientation: **3**.

---

## Execute

Purpose:

> Complete one bounded task.

The run receives:

- task
- mission
- relevant context
- approved workspace
- definition of done
- policy
- relevant skills/scripts
- required evidence

The run should remain focused on that task.

---

## Review

Purpose:

> Verify or critique important completed work.

A review may inspect:

- diff
- files
- tests
- screenshots
- output artifacts
- task definition of done

Review is used selectively, not for every trivial task.

---

# Task Packet

Every Codex execution receives a generated task packet.

Example:

```markdown
# Task

Prepare a landing page for AI Funnel Design Review.

## Mission

Grow qualified traffic, product usage, and paid adoption across Wynter.ai.

## Why

Search data indicates demand for this topic and no targeted page exists.

## Workspace

~/Code/wynter-ai

## Definition of Done

- Create the page
- Add metadata
- Add two internal links
- Build succeeds
- Generate desktop screenshot

## Policy

Allowed:
- read/write approved workspace
- run tests
- create local branch
- create local commit

Requires approval:
- deploy production
- publish content
- merge protected branch

## Required Result

Return:
- concise summary
- files changed
- verification performed
- remaining uncertainty
- approval needed
```

---

# Codex Process Rules

Each run must have:

- explicit working directory
- explicit task
- explicit allowed workspace
- timeout
- cancellation support
- maximum retry count
- captured output
- final status

Nabu must be able to kill a running Codex process.

---

# Failure Handling

If Codex fails:

1. Record the failure.
2. Decide whether the failure is retryable.
3. Retry once automatically when appropriate.
4. If it fails again, mark the task `Failed` or `Needs Attention`.
5. Surface the issue on Overview.
6. Do not loop indefinitely.

---

# Codex Unavailable

If Codex is unavailable or rate limited:

- Nabu remains running
- scheduled non-AI scripts continue
- queue remains intact
- active AI work becomes waiting
- UI shows a clear status
- Nabu retries availability conservatively
- no task state is lost

---

# Task Creation

Tasks can come from:

- the user
- chat
- orientation
- a schedule
- a script result
- a completed task
- a failed task

Every task requires:

```text
title
purpose
status
priority
definition_of_done
created_by
created_at
```

Optional:

```text
workspace
schedule
context
approval_requirement
parent_task
artifacts
```

---

# Priorities

Use only:

```text
High
Normal
Low
```

Do not implement complicated numeric prioritization.

---

# Work-In-Progress Limits

Default:

```text
Running AI tasks: 1
Ready tasks: 5
Active task ideas: unlimited but hidden from active queue
```

Nabu may execute deterministic scripts concurrently where safe.

The default experience should favor one focused Codex task at a time.

---

# Reorientation

Nabu should request orientation when:

- mission starts
- queue becomes empty
- important work completes
- important work fails
- user significantly changes direction
- meaningful new signal appears
- configured orientation schedule fires

Do not run constant AI heartbeats.

---

# Scheduler

Nabu includes a small durable scheduler.

A schedule can trigger:

- local script
- task creation
- orientation

Examples:

```text
Run site-health script every hour.
Create weekly search review task.
Run analytics snapshot each morning.
Orient if the queue is empty.
```

Schedules survive restart.

---

# Scripts

Scripts are the preferred way to perform repeatable deterministic work.

Workspace:

```text
~/.nabu/workspace/scripts/
```

Examples:

```text
analytics-summary
search-console-export
site-health
sentry-errors
railway-status
git-status
```

A script should produce structured output where practical.

Example:

```json
{
  "status": "ok",
  "summary": "Traffic increased 12%.",
  "data": {},
  "interesting": true
}
```

If `interesting` is true, Nabu may create a task or request orientation.

---

# Skills

Skills provide reusable instructions to Codex.

Workspace:

```text
~/.nabu/workspace/skills/
```

Example:

```text
skills/
└── search-opportunity/
    ├── SKILL.md
    ├── references/
    └── scripts/
```

Nabu can make approved skills available to Codex in the workspace.

Skills remain simple filesystem assets.

No marketplace or plugin system is required.

---

# Memory

Memory is intentionally simple.

## MEMORY.md

Curated durable information.

Examples:

- product positioning
- important decisions
- recurring constraints
- business terminology
- stable preferences
- lessons that should affect future work

---

## Daily Memory

```text
memory/YYYY-MM-DD.md
```

Contains concise operational notes.

---

## SQLite

Structured operational truth lives in SQLite.

Do not use Markdown as the authoritative source for:

- task status
- run status
- approvals
- schedules
- reports
- events

---

# Context Files

Default:

```text
NABU.md
MISSION.md
BUSINESS.md
USER.md
POLICY.md
MEMORY.md
```

## NABU.md

Defines Nabu's operating character.

Suggested core:

```markdown
# Nabu

You are Nabu, an autonomous operator responsible for advancing
the active mission.

Be useful, focused, evidence-driven, and conservative with
irreversible actions.

Prefer completing a small number of high-value tasks over
creating a large backlog.

Do not claim work succeeded without checking it.

Ask the owner only when policy requires approval or important
direction cannot be derived from existing context.

Communicate as one operator. Internal Codex runs are implementation
details.
```

---

# Policy

Policy must remain understandable in one screen.

Store policy in structured data and render a readable version into task context.

Core action categories:

```text
Read
Work
Publish
Dangerous
```

## Read

Default: allow.

Includes:

- read files
- inspect websites
- inspect repositories
- read metrics
- web research

---

## Work

Default: allow in approved workspaces.

Includes:

- edit files
- run tests
- run scripts
- create branches
- create commits
- generate drafts
- generate reports

---

## Publish

Default: ask.

Includes:

- merge
- deploy production
- publish public content
- send external communication
- modify production configuration

---

## Dangerous

Always ask.

Includes:

- delete production data
- modify authentication
- modify billing
- spend money
- change security credentials
- destructive infrastructure actions

---

# Approvals

Approvals should be simple.

Approval states:

```text
Pending
Approved
Rejected
Expired
```

Approval card contains:

- proposed action
- why
- related task
- what will change
- evidence/preview
- Approve
- Reject

Optional rejection comment.

Example:

```text
Publish landing page

Nabu completed the page and verification passed.

Changes:
- 1 new page
- 2 internal links
- sitemap updated

[Review Changes]

[Reject] [Approve]
```

Once approved, Nabu resumes the paused task.

---

# Reports

Reports are durable outputs, not chat messages.

Types can remain unstructured initially.

Examples:

- daily mission report
- research report
- SEO report
- investigation
- conversion review
- deployment review

Each report has:

```text
title
summary
created_at
related_tasks
body
artifacts
```

---

# Daily Mission Report

Nabu should be able to produce a concise report containing:

```text
What changed
What was completed
What failed
What was learned
What needs the user
What Nabu is doing next
```

Do not force a report if nothing meaningful happened.

---

# Local API

`nabud` exposes a localhost API used by the React UI.

Initial API groups:

```text
/api/status
/api/mission
/api/tasks
/api/runs
/api/reports
/api/chat
/api/approvals
/api/settings
/api/events
```

Use JSON.

Use Server-Sent Events for live updates.

WebSocket is unnecessary unless a feature genuinely requires bidirectional persistent transport.

---

# Live Events

Example event types:

```text
status.changed
task.created
task.updated
task.started
task.completed
task.failed
run.output
run.completed
approval.created
approval.resolved
report.created
chat.message
```

The React app subscribes and updates without refreshing.

---

# Persistence

Use SQLite.

Suggested tables:

```text
settings
missions
tasks
runs
events
approvals
reports
messages
schedules
artifacts
```

Use migrations from the beginning.

---

# File Storage

Large or human-readable artifacts live on disk.

Example:

```text
~/.nabu/
├── workspace/
├── artifacts/
├── reports/
├── runs/
└── logs/
```

SQLite stores metadata and paths.

---

# Repository Handling

The user explicitly adds repositories.

Nabu records:

```text
name
path
default_branch
allowed
```

For coding tasks:

- work inside the repository
- prefer task-specific Git branches
- do not modify unrelated repositories
- capture Git diff
- leave repository in a recoverable state

Git worktrees are optional, not required for the first implementation.

One running Codex task at a time greatly reduces the need for worktree orchestration.

---

# Browser and Web Work

Nabu should support browser-oriented work through local scripts and Codex-compatible tooling.

Prefer:

1. HTTP/API/scripts
2. structured page fetching
3. Playwright for browser interaction
4. manual user takeover when authentication/CAPTCHA blocks automation

Browser functionality should be exposed through task activity and artifacts, not as a separate primary product area.

If a browser run produces screenshots, show them in the task detail and chat.

---

# Pause and Resume

Global **Pause** must be prominent.

Pause means:

- do not start new Codex work
- do not begin publish actions
- allow current safe task to be cancelled or finish based on user choice
- retain queue
- retain schedules
- continue basic daemon health

Resume continues from durable state.

---

# Status

Global Nabu states:

```text
Working
Idle
Waiting for Approval
Paused
Needs Attention
```

Only one global state is displayed.

Detailed process states remain inside task views.

---

# Service Operation

## macOS

Install `nabud` as a user LaunchAgent.

## Linux

Install `nabud` as a user systemd service.

Requirements:

- start automatically
- restart on crash
- retain SQLite state
- retain logs
- serve web UI after restart
- recover interrupted task state

---

# CLI

The CLI exists only for setup and emergency control.

Required commands:

```bash
nabu setup
nabu open
nabu status
nabu start
nabu stop
nabu restart
nabu logs
nabu doctor
nabu uninstall
```

Normal daily use happens in the web UI.

---

# `nabu doctor`

Check:

- daemon
- SQLite
- frontend assets
- Codex CLI
- Codex authentication availability
- Git
- workspace permissions
- approved repository paths
- disk space
- local port
- service installation

Output should be concise and actionable.

---

# Security

Default server binding:

```text
127.0.0.1
```

Do not expose Nabu publicly.

Secrets must not be written into:

- prompts
- chat history
- task descriptions
- reports
- logs

Scripts should own service credentials where possible and return only required data.

Approved workspaces must be explicit.

Nabu must never silently broaden filesystem scope.

---

# Logging

Keep two levels.

## Product Activity

Human-readable events shown in UI.

## Debug Logs

Technical daemon/process logs stored on disk.

Do not fill the normal UI with raw infrastructure logs.

---

# Result Model

A completed Codex run should return a normalized result.

```json
{
  "status": "completed",
  "summary": "Created and verified the landing page.",
  "files_changed": [],
  "verification": [],
  "artifacts": [],
  "uncertainties": [],
  "approval_needed": null
}
```

Nabu stores the normalized result even if raw Codex output is also preserved.

---

# Phase 1 — Local Shell

## Goal

Install Nabu, run the Go daemon, open the embedded dark React interface, and persist basic state.

## Tasks

- [ ] Create Go project
- [ ] Create `nabu` CLI
- [ ] Create `nabud` daemon
- [ ] Add config directory at `~/.nabu`
- [ ] Add SQLite
- [ ] Add migrations
- [ ] Create React + TypeScript frontend
- [ ] Add Vite
- [ ] Add Tailwind CSS
- [ ] Add shadcn/ui
- [ ] Implement dark-mode design tokens
- [ ] Build frontend into static assets
- [ ] Embed frontend assets into Go binary
- [ ] Add local HTTP server
- [ ] Bind to `127.0.0.1`
- [ ] Add `/api/status`
- [ ] Add initial Overview shell
- [ ] Add sidebar navigation
- [ ] Add global status
- [ ] Add Pause button shell
- [ ] Add `nabu open`
- [ ] Add `nabu status`
- [ ] Add macOS LaunchAgent installation
- [ ] Add Linux systemd user service installation
- [ ] Recover cleanly after daemon restart

## Result

Running Nabu opens a polished local dark web application and the daemon survives after the browser closes.

---

# Phase 2 — Setup and Mission

## Goal

A user can install Nabu, connect the local Codex CLI, define the mission, and select approved workspaces.

## Tasks

- [ ] Build first-run setup flow
- [ ] Detect Codex binary
- [ ] Verify Codex can execute
- [ ] Detect Git
- [ ] Create workspace structure
- [ ] Create `NABU.md`
- [ ] Create `MISSION.md`
- [ ] Create `BUSINESS.md`
- [ ] Create `USER.md`
- [ ] Create `POLICY.md`
- [ ] Create `MEMORY.md`
- [ ] Build mission editor
- [ ] Build context editor
- [ ] Build repository/folder picker
- [ ] Store approved workspace paths
- [ ] Build simple autonomy setup
- [ ] Show setup summary
- [ ] Add Start Mission action
- [ ] Render active mission on Overview

## Result

A new install can reach a working mission without manually editing configuration files.

---

# Phase 3 — Codex Runner

## Goal

Nabu can safely launch, observe, cancel, and record Codex runs without tmux.

## Tasks

- [ ] Implement Go child-process supervisor
- [ ] Start Codex with explicit working directory
- [ ] Capture stdout
- [ ] Capture stderr
- [ ] Capture structured output where available
- [ ] Record PID
- [ ] Record start/end timestamps
- [ ] Record exit code
- [ ] Add cancellation
- [ ] Add timeout
- [ ] Add one automatic retry
- [ ] Persist runs
- [ ] Restore interrupted run state after restart
- [ ] Normalize run result
- [ ] Stream run events through SSE
- [ ] Display active run on Overview
- [ ] Build raw output view
- [ ] Build human-readable activity view

## Result

Nabu can reliably treat Codex as a supervised local worker.

---

# Phase 4 — Task Queue

## Goal

Nabu can create, prioritize, run, complete, fail, and cancel durable tasks.

## Tasks

- [ ] Create task database model
- [ ] Implement task states
- [ ] Implement High/Normal/Low priority
- [ ] Add definition of done
- [ ] Add task purpose
- [ ] Add workspace association
- [ ] Add task creation UI
- [ ] Add task list
- [ ] Add task detail
- [ ] Add Ready queue
- [ ] Add one-task AI concurrency limit
- [ ] Dispatch Ready task to Codex
- [ ] Generate task packet
- [ ] Store run/task relationship
- [ ] Handle successful completion
- [ ] Handle failure
- [ ] Handle cancellation
- [ ] Capture artifacts
- [ ] Capture Git diff when applicable
- [ ] Show output and verification
- [ ] Update Overview queue automatically

## Result

Nabu can continuously work through a small durable queue.

---

# Phase 5 — Orientation Loop

## Goal

Nabu can reason about the mission and create useful next tasks without constant AI heartbeats.

## Tasks

- [ ] Implement Orient run type
- [ ] Build orientation prompt/context packet
- [ ] Include mission
- [ ] Include business context
- [ ] Include recent completed work
- [ ] Include failures
- [ ] Include current queue
- [ ] Include recent meaningful events
- [ ] Require structured orientation output
- [ ] Limit new Ready tasks to 3 per orientation
- [ ] Deduplicate proposed tasks
- [ ] Allow orientation to reprioritize
- [ ] Allow orientation to decide no work is needed
- [ ] Trigger orientation when queue is empty
- [ ] Trigger orientation after meaningful completion
- [ ] Trigger orientation after significant user steering
- [ ] Prevent orientation loops
- [ ] Display why a task was created

## Result

Nabu can keep the mission moving without the user manually feeding it every task.

---

# Phase 6 — Chat

## Goal

The user can steer Nabu through a polished chat experience while durable state remains authoritative.

## Tasks

- [ ] Create Chat page
- [ ] Create persistent conversation
- [ ] Store messages in SQLite
- [ ] Stream Codex responses
- [ ] Render Markdown
- [ ] Render code blocks
- [ ] Add copy controls
- [ ] Add scroll behavior
- [ ] Add loading/working states
- [ ] Add compact global composer on Overview
- [ ] Add task reference cards
- [ ] Add report reference cards
- [ ] Add approval cards
- [ ] Add screenshot/image rendering
- [ ] Adapt useful UX patterns/components from Vercel Chatbot
- [ ] Remove model selector concepts
- [ ] Remove provider concepts
- [ ] Remove hosted auth concepts
- [ ] Classify chat effects
- [ ] Allow chat to create tasks
- [ ] Allow chat to reprioritize tasks
- [ ] Allow chat to cancel tasks
- [ ] Allow chat to update mission
- [ ] Allow chat to update durable context
- [ ] Allow chat to update policy
- [ ] Allow chat to pause/resume
- [ ] Confirm state-changing effects in Nabu's response
- [ ] Refresh corresponding UI immediately

## Result

Chat feels powerful, but it steers a real operator rather than acting as a disconnected chatbot.

---

# Phase 7 — Policy and Approvals

## Goal

Nabu can work independently while important external actions remain understandable and controlled.

## Tasks

- [ ] Implement Read/Work/Publish/Dangerous categories
- [ ] Build policy settings screen
- [ ] Set sensible defaults
- [ ] Include current policy in task packets
- [ ] Implement approval records
- [ ] Implement Pending/Approved/Rejected/Expired
- [ ] Pause task at approval boundary
- [ ] Build Overview approval card
- [ ] Build approval detail
- [ ] Show proposed change
- [ ] Show related evidence
- [ ] Add Approve
- [ ] Add Reject
- [ ] Add optional rejection note
- [ ] Resume task after approval
- [ ] Store approval history
- [ ] Prevent Codex text from bypassing daemon policy

## Result

The user can leave Nabu running without surrendering control of high-impact actions.

---

# Phase 8 — Reports and Memory

## Goal

Nabu remembers important operational context and produces useful durable summaries.

## Tasks

- [ ] Create reports model
- [ ] Create Reports page
- [ ] Allow Codex tasks to produce reports
- [ ] Link reports to tasks
- [ ] Link artifacts to reports
- [ ] Build concise report viewer
- [ ] Add daily mission report capability
- [ ] Create daily memory file support
- [ ] Create durable `MEMORY.md`
- [ ] Add memory/context update action
- [ ] Let Codex propose memory changes
- [ ] Require Nabu to store operational state in SQLite
- [ ] Keep memory concise
- [ ] Include relevant memory in orientation
- [ ] Include relevant memory in task packets
- [ ] Show latest meaningful results on Overview

## Result

Nabu can restart, reorient, and continue operating with useful continuity.

---

# Phase 9 — Scheduler and Scripts

## Goal

Repeatable checks run without unnecessarily invoking Codex.

## Tasks

- [ ] Create schedule model
- [ ] Persist schedules
- [ ] Run schedules after restart
- [ ] Support script trigger
- [ ] Support task trigger
- [ ] Support orientation trigger
- [ ] Add script registry
- [ ] Support structured script output
- [ ] Capture script logs
- [ ] Capture script artifacts
- [ ] Let interesting script results create tasks
- [ ] Add simple schedule settings UI
- [ ] Add site-health example
- [ ] Add analytics-summary example
- [ ] Add Search Console example
- [ ] Ensure idle Nabu consumes no AI calls

## Result

Nabu can monitor a mission continuously while reserving Codex for actual reasoning and work.

---

# Phase 10 — Operational Reliability

## Goal

Nabu can be trusted as a 24/7 local service.

## Tasks

- [ ] Implement global pause/resume
- [ ] Implement clean shutdown
- [ ] Recover interrupted tasks
- [ ] Handle Codex unavailable state
- [ ] Handle rate-limit state
- [ ] Prevent infinite retries
- [ ] Add task/run timeouts
- [ ] Add daemon crash recovery
- [ ] Add database backup
- [ ] Add log rotation
- [ ] Add disk-space check
- [ ] Add `nabu doctor`
- [ ] Add actionable error messages
- [ ] Add UI Needs Attention state
- [ ] Add service restart controls
- [ ] Add safe uninstall
- [ ] Preserve user workspace on uninstall unless explicitly requested
- [ ] Test computer restart recovery
- [ ] Test browser closure
- [ ] Test Codex crash
- [ ] Test failed task
- [ ] Test rejected approval
- [ ] Test paused mission
- [ ] Test empty queue orientation

## Result

Nabu can remain installed and running continuously with predictable recovery behavior.

---

# Definition of Product Complete

Nabu is complete when the following experience works end to end:

1. User installs Nabu.
2. Nabu detects authenticated Codex CLI.
3. Browser opens the local dark UI.
4. User enters a mission.
5. User provides basic business context.
6. User approves one or more workspaces.
7. User accepts simple autonomy defaults.
8. User starts the mission.
9. Nabu performs orientation.
10. Nabu creates a small task queue.
11. Nabu executes the highest-priority task with Codex.
12. UI shows what Nabu is doing.
13. Task activity streams into the UI.
14. Nabu verifies and records the result.
15. Nabu creates a report when meaningful.
16. Nabu asks when an action crosses an approval boundary.
17. User approves or rejects from the web UI.
18. Nabu continues the task appropriately.
19. Nabu reorients when useful.
20. User can steer priorities through chat.
21. Chat changes the actual queue/mission/policy.
22. Browser can be closed without stopping Nabu.
23. Computer can restart without losing mission state.
24. Nabu can remain idle without consuming Codex usage.
25. Nabu resumes useful work when work becomes available.

---

# Final Product Shape

```text
                         NABU

                  One AI on a Mission

                           │
                           ▼

                  Dark React Web UI

        Overview · Tasks · Reports · Chat · Settings

                           │
                           ▼

                         nabud
                      Go daemon

         Mission · Queue · Policy · Scheduler · Memory
            Runs · Reports · Approvals · SQLite

                           │
                           ▼

                       Codex CLI

                           │
                           ▼

           Repositories · Scripts · Browser · Git
```

Nabu should remain intentionally small.

The product is not an AI development framework.

It is not a multi-agent platform.

It is not a hosted chatbot.

It is not a terminal multiplexer.

It is a persistent local operator:

> **Give Nabu a mission. It keeps the work moving.**
