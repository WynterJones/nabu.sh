# Nabu operations

Nabu is a localhost-only application. Its durable state lives under `~/.nabu`
by default. Set `NABU_HOME` to use an isolated development or test instance.

## Run a local development instance

Build the embedded frontend and both Go binaries:

```sh
make build
```

Start Nabu with isolated data, then open <http://127.0.0.1:7777>:

```sh
NABU_HOME="$PWD/.nabu-dev" ./bin/nabud
```

Stop it with `Ctrl-C`. The daemon waits for HTTP requests, scripts, and backups
to stop before closing SQLite. Starting the same command again recovers any
interrupted runs and durable schedule leases.

For frontend-only iteration, keep `nabud` running and use Vite in another
terminal:

```sh
cd frontend
npm run dev
```

## Verify a build

```sh
go test -race ./...
go vet ./...
cd frontend
npm run typecheck
npm run lint
npm test -- --run
npm run build
```

## Install the user service

Install from a checkout:

```sh
./install.sh
nabu setup
```

Service controls preserve `~/.nabu`:

```sh
nabu status
nabu logs
nabu doctor
nabu restart
nabu stop
nabu start
```

`nabu uninstall` removes only the LaunchAgent/systemd user unit. It does not
remove Nabu's database, context, artifacts, reports, run logs, or backups.

## Backups and recovery

The daemon creates and integrity-checks one SQLite backup per UTC day in:

```text
~/.nabu/backups/nabu-YYYY-MM-DD.db
```

Before restoring, stop Nabu and preserve the current database. Replace paths
below if `NABU_HOME` is configured:

```sh
nabu stop
NABU_DATA_DIR="$HOME/.nabu"
NABU_RECOVERY_DIR="$NABU_DATA_DIR/recovery-before-restore"
mkdir -p "$NABU_RECOVERY_DIR"
mv "$NABU_DATA_DIR/nabu.db" "$NABU_RECOVERY_DIR/nabu.db"
test ! -e "$NABU_DATA_DIR/nabu.db-wal" || mv "$NABU_DATA_DIR/nabu.db-wal" "$NABU_RECOVERY_DIR/nabu.db-wal"
test ! -e "$NABU_DATA_DIR/nabu.db-shm" || mv "$NABU_DATA_DIR/nabu.db-shm" "$NABU_RECOVERY_DIR/nabu.db-shm"
cp "$NABU_DATA_DIR/backups/nabu-YYYY-MM-DD.db" "$NABU_DATA_DIR/nabu.db"
nabu start
nabu doctor
```

Do not restore while the daemon is running. Keep the recovery directory
until `nabu doctor` and the local UI both confirm the recovered state.

## Schedule payloads

Task schedules require a title and at least one definition-of-done item:

```json
{
  "title": "Review weekly search performance",
  "purpose": "Identify meaningful changes and opportunities.",
  "priority": "normal",
  "definition_of_done": ["Record verified findings and next actions."]
}
```

Script schedules reference a registered script. An interesting result may
create a bounded task, request orientation, or remain informational:

```json
{
  "script_id": "registered-script-id",
  "on_interesting": "task"
}
```

Orientation schedules may use an empty payload or a short reason:

```json
{
  "reason": "Daily mission review"
}
```

Routine, non-interesting script results only persist their run, output, and
artifacts. They do not invoke Codex.
