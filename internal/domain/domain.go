package domain

import (
	"encoding/json"
	"time"
)

type GlobalStatus string

const (
	GlobalWorking            GlobalStatus = "working"
	GlobalIdle               GlobalStatus = "idle"
	GlobalWaitingForApproval GlobalStatus = "waiting_for_approval"
	GlobalPaused             GlobalStatus = "paused"
	GlobalNeedsAttention     GlobalStatus = "needs_attention"
)

type TaskStatus string

const (
	TaskIdea          TaskStatus = "idea"
	TaskReady         TaskStatus = "ready"
	TaskRunning       TaskStatus = "running"
	TaskWaiting       TaskStatus = "waiting"
	TaskNeedsApproval TaskStatus = "needs_approval"
	TaskCompleted     TaskStatus = "completed"
	TaskFailed        TaskStatus = "failed"
	TaskCancelled     TaskStatus = "cancelled"
)

type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

type RunType string

const (
	RunOrient   RunType = "orient"
	RunExecute  RunType = "execute"
	RunReview   RunType = "review"
	RunChat     RunType = "chat"
	RunTypeChat         = RunChat
)

type RunStatus string

const (
	RunPending     RunStatus = "pending"
	RunRunning     RunStatus = "running"
	RunCompleted   RunStatus = "completed"
	RunFailed      RunStatus = "failed"
	RunCancelled   RunStatus = "cancelled"
	RunTimedOut    RunStatus = "timed_out"
	RunInterrupted RunStatus = "interrupted"
)

type Mission struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Statement   string    `json:"statement"`
	Context     string    `json:"context"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Workspace struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Path              string     `json:"path"`
	IconPath          string     `json:"-"`
	IconURL           string     `json:"icon_url,omitempty"`
	DefaultBranch     string     `json:"default_branch,omitempty"`
	Allowed           bool       `json:"allowed"`
	MissionStarted    bool       `json:"mission_started"`
	ContextReady      bool       `json:"context_ready"`
	ContextPrompted   bool       `json:"-"`
	OrientationQueued bool       `json:"orientation_queued"`
	LastOrientationAt *time.Time `json:"last_orientation_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	Active            bool       `json:"active,omitempty"`
}

type DefinitionItem struct {
	Text      string `json:"text"`
	Completed bool   `json:"completed"`
	Failed    bool   `json:"failed,omitempty"`
	Details   string `json:"details,omitempty"`
}

type DefinitionOutcome struct {
	Text    string `json:"text"`
	Status  string `json:"status"`
	Details string `json:"details,omitempty"`
}

type Task struct {
	ID               string           `json:"id"`
	Title            string           `json:"title"`
	Purpose          string           `json:"purpose"`
	Why              string           `json:"why"`
	Status           TaskStatus       `json:"status"`
	Priority         Priority         `json:"priority"`
	DefinitionOfDone []DefinitionItem `json:"definition_of_done"`
	DependsOnTaskIDs []string         `json:"depends_on_task_ids"`
	WorkspaceID      string           `json:"workspace_id,omitempty"`
	Workspace        *Workspace       `json:"workspace,omitempty"`
	CreatedBy        string           `json:"created_by"`
	ParentTaskID     string           `json:"parent_task_id,omitempty"`
	CurrentRunID     string           `json:"current_run_id,omitempty"`
	Result           *RunResult       `json:"result,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	PlannedAt        *time.Time       `json:"planned_at,omitempty"`
	RunRequestedAt   *time.Time       `json:"run_requested_at,omitempty"`
	StartedAt        *time.Time       `json:"started_at,omitempty"`
	CompletedAt      *time.Time       `json:"completed_at,omitempty"`
}

type Verification struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Details string `json:"details,omitempty"`
}

type Artifact struct {
	ID          string          `json:"id"`
	TaskID      string          `json:"task_id,omitempty"`
	RunID       string          `json:"run_id,omitempty"`
	ScriptRunID string          `json:"script_run_id,omitempty"`
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Path        string          `json:"path,omitempty"`
	URL         string          `json:"url,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type RunResult struct {
	Status         string                 `json:"status"`
	Summary        string                 `json:"summary"`
	DefinitionDone []DefinitionOutcome    `json:"definition_of_done,omitempty"`
	FilesChanged   []string               `json:"files_changed"`
	Verification   []Verification         `json:"verification"`
	Artifacts      []Artifact             `json:"artifacts"`
	Uncertainties  []string               `json:"uncertainties"`
	ApprovalNeeded *string                `json:"approval_needed"`
	DatasetWrites  []DatasetWrite         `json:"dataset_writes,omitempty"`
	LocalApps      []LocalAppRegistration `json:"local_apps,omitempty"`
}

// LocalAppRegistration lets a completed, verified task surface a runnable
// browser application without hiding it in the workspace filesystem. Command
// is an argv vector and is never evaluated by a shell.
type LocalAppRegistration struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Directory   string   `json:"directory"`
	Command     []string `json:"command"`
	Port        int      `json:"port"`
	HealthPath  string   `json:"health_path,omitempty"`
	AutoStart   bool     `json:"auto_start,omitempty"`
	Applied     bool     `json:"applied,omitempty"`
}

type DatasetWriteOperation string

const (
	DatasetWriteCreate DatasetWriteOperation = "create_dataset"
	DatasetWriteUpsert DatasetWriteOperation = "upsert_rows"
	DatasetWriteUpdate DatasetWriteOperation = "update_row"
	DatasetWriteDelete DatasetWriteOperation = "delete_row"
)

// DatasetWrite is a bounded, typed request from an autonomous task. Applied
// results retain metadata and counts but rows are cleared before persistence.
type DatasetWrite struct {
	Operation DatasetWriteOperation `json:"operation"`
	DatasetID string                `json:"dataset_id,omitempty"`
	Dataset   *Dataset              `json:"dataset,omitempty"`
	Rows      []map[string]any      `json:"rows,omitempty"`
	RowsFile  string                `json:"rows_file,omitempty"`
	RowID     int64                 `json:"row_id,omitempty"`
	Values    map[string]any        `json:"values,omitempty"`
	Applied   bool                  `json:"applied,omitempty"`
	Inserted  int                   `json:"inserted,omitempty"`
	Updated   int                   `json:"updated,omitempty"`
}

type Run struct {
	ID               string     `json:"id"`
	TaskID           string     `json:"task_id,omitempty"`
	Type             RunType    `json:"type"`
	Status           RunStatus  `json:"status"`
	PID              int        `json:"pid,omitempty"`
	WorkingDirectory string     `json:"working_directory"`
	Command          []string   `json:"command,omitempty"`
	SessionID        string     `json:"session_id,omitempty"`
	Attempt          int        `json:"attempt"`
	StartedAt        time.Time  `json:"started_at"`
	EndedAt          *time.Time `json:"ended_at,omitempty"`
	ExitCode         *int       `json:"exit_code,omitempty"`
	StdoutPath       string     `json:"stdout_path,omitempty"`
	StderrPath       string     `json:"stderr_path,omitempty"`
	Stdout           string     `json:"stdout,omitempty"`
	Stderr           string     `json:"stderr,omitempty"`
	RawOutput        string     `json:"raw_output,omitempty"`
	Result           *RunResult `json:"result,omitempty"`
	Error            string     `json:"error,omitempty"`
	Events           []RunEvent `json:"events,omitempty"`
}

type RunEvent struct {
	ID      string    `json:"id,omitempty"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
	Stream  string    `json:"stream,omitempty"`
}

type Event struct {
	ID          int64           `json:"id"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Type        string          `json:"type"`
	EntityID    string          `json:"entity_id,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type Settings struct {
	DisplayName          string     `json:"display_name"`
	SetupComplete        bool       `json:"setup_complete"`
	Paused               bool       `json:"paused"`
	MissionStarted       bool       `json:"mission_started"`
	CodexPath            string     `json:"codex_path,omitempty"`
	GitPath              string     `json:"git_path,omitempty"`
	ServerAddress        string     `json:"server_address"`
	OrientationQueued    bool       `json:"orientation_queued"`
	LastOrientationAt    *time.Time `json:"last_orientation_at,omitempty"`
	ActiveWorkspaceID    string     `json:"active_workspace_id,omitempty"`
	LastBackupAt         *time.Time `json:"last_backup_at,omitempty"`
	CodexModel           string     `json:"codex_model,omitempty"`
	CodexReasoningEffort string     `json:"codex_reasoning_effort,omitempty"`
	MaxParallelTasks     int        `json:"max_parallel_tasks"`
}

type Policy struct {
	Read      string `json:"read"`
	Work      string `json:"work"`
	Publish   string `json:"publish"`
	Dangerous string `json:"dangerous"`
}

type MessageRole string

const (
	MessageUser      MessageRole = "user"
	MessageAssistant MessageRole = "assistant"
	MessageSystem    MessageRole = "system"
)

type MessageStatus string

const (
	MessageQueued     MessageStatus = "queued"
	MessageProcessing MessageStatus = "processing"
	MessageComplete   MessageStatus = "complete"
	MessageFailed     MessageStatus = "failed"
)

type ChatEffect string

const (
	EffectConversationOnly  ChatEffect = "conversation_only"
	EffectProposeContext    ChatEffect = "propose_context_completion"
	EffectRequestChoice     ChatEffect = "request_choice"
	EffectCompleteContext   ChatEffect = "complete_context"
	EffectCreateTask        ChatEffect = "create_task"
	EffectUpdateTask        ChatEffect = "update_task"
	EffectCancelTask        ChatEffect = "cancel_task"
	EffectUpdateMission     ChatEffect = "update_mission"
	EffectUpdateContext     ChatEffect = "update_context"
	EffectUpdatePolicy      ChatEffect = "update_policy"
	EffectApproveAction     ChatEffect = "approve_action"
	EffectRejectAction      ChatEffect = "reject_action"
	EffectPause             ChatEffect = "pause"
	EffectResume            ChatEffect = "resume"
	EffectRequestReport     ChatEffect = "request_report"
	EffectCreatePlan        ChatEffect = "create_plan"
	EffectCreateSchedule    ChatEffect = "create_schedule"
	EffectCreateIntegration ChatEffect = "create_integration"
	EffectCreateSecret      ChatEffect = "create_secret"
	EffectCreateScript      ChatEffect = "create_script"
	EffectCreateDataset     ChatEffect = "create_dataset"
	EffectUpsertDatasetRows ChatEffect = "upsert_dataset_rows"
	EffectUpdateDatasetRow  ChatEffect = "update_dataset_row"
	EffectDeleteDatasetRow  ChatEffect = "delete_dataset_row"
	EffectUpdateSoul        ChatEffect = "update_soul"
)

// Message belongs to Nabu's single primary conversation. Effect and
// EffectMetadata describe durable state changes caused by the message.
type Message struct {
	ID              int64           `json:"id"`
	WorkspaceID     string          `json:"workspace_id,omitempty"`
	ParentMessageID *int64          `json:"parent_message_id,omitempty"`
	ThreadRootID    *int64          `json:"thread_root_id,omitempty"`
	ReplyCount      int             `json:"reply_count,omitempty"`
	Role            MessageRole     `json:"role"`
	Content         string          `json:"content"`
	Status          MessageStatus   `json:"status"`
	Effect          ChatEffect      `json:"effect"`
	EffectMetadata  json.RawMessage `json:"effect_metadata,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalExpired  ApprovalStatus = "expired"
)

type Approval struct {
	ID               string          `json:"id"`
	WorkspaceID      string          `json:"workspace_id,omitempty"`
	TaskID           string          `json:"task_id,omitempty"`
	RunID            string          `json:"run_id,omitempty"`
	Status           ApprovalStatus  `json:"status"`
	ProposedAction   string          `json:"proposed_action"`
	Why              string          `json:"why"`
	ProposedChange   string          `json:"proposed_change"`
	Evidence         string          `json:"evidence,omitempty"`
	EvidenceMetadata json.RawMessage `json:"evidence_metadata,omitempty"`
	RejectionNote    string          `json:"rejection_note,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	ExpiresAt        *time.Time      `json:"expires_at,omitempty"`
	ResolvedAt       *time.Time      `json:"resolved_at,omitempty"`
}

type ReportStatus string

const (
	ReportUnread   ReportStatus = "unread"
	ReportRead     ReportStatus = "read"
	ReportArchived ReportStatus = "archived"
)

type Report struct {
	ID             string       `json:"id"`
	WorkspaceID    string       `json:"workspace_id,omitempty"`
	Kind           string       `json:"kind,omitempty"`
	Status         ReportStatus `json:"status"`
	Title          string       `json:"title"`
	Summary        string       `json:"summary"`
	Body           string       `json:"body"`
	Path           string       `json:"path,omitempty"`
	RelatedTaskIDs []string     `json:"related_task_ids"`
	ArtifactIDs    []string     `json:"artifact_ids"`
	Artifacts      []Artifact   `json:"artifacts,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

type ScheduleKind string

const (
	ScheduleScript ScheduleKind = "script"
	ScheduleTask   ScheduleKind = "task"
	ScheduleOrient ScheduleKind = "orient"
)

// Schedule supports either a fixed IntervalSeconds or a simple scheduler
// expression interpreted by the daemon. Claim fields form a durable lease.
type Schedule struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id,omitempty"`
	Name            string          `json:"name"`
	Enabled         bool            `json:"enabled"`
	Kind            ScheduleKind    `json:"kind"`
	Expression      string          `json:"expression,omitempty"`
	IntervalSeconds int64           `json:"interval_seconds,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	LastRunAt       *time.Time      `json:"last_run_at,omitempty"`
	NextRunAt       *time.Time      `json:"next_run_at,omitempty"`
	ClaimToken      string          `json:"claim_token,omitempty"`
	ClaimedAt       *time.Time      `json:"claimed_at,omitempty"`
	LeaseUntil      *time.Time      `json:"lease_until,omitempty"`
	LastError       string          `json:"last_error,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	Cadence         ScheduleCadence `json:"cadence"`
}

type ScheduleCadence struct {
	Expression      string `json:"expression,omitempty"`
	IntervalSeconds int64  `json:"interval_seconds,omitempty"`
}

// SecretCredentialIntegration is the fixed credentials.Backend namespace for
// generic workspace secrets. Keeping it constant prevents records from
// redirecting a binding into another credential namespace.
const SecretCredentialIntegration = "secrets"

// SecretRecord contains discoverable metadata only. ReferenceKey identifies
// the entry in credentials.Backend; secret values must never be added here or
// persisted in SQLite.
type SecretRecord struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id,omitempty"`
	Name         string    `json:"name"`
	Label        string    `json:"label,omitempty"`
	Description  string    `json:"description,omitempty"`
	ReferenceKey string    `json:"reference_key"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ScriptCredentialBinding maps a safe child-process environment variable to
// a SecretRecord. CredentialIntegration and CredentialName are hydrated from
// the record for runtime use and are not independently persisted.
type ScriptCredentialBinding struct {
	Env                   string `json:"env_var"`
	SecretRecordID        string `json:"secret_id"`
	CredentialIntegration string `json:"-"`
	CredentialName        string `json:"-"`
}

type ScriptAccess string

const (
	ScriptAccessRead  ScriptAccess = "read"
	ScriptAccessWrite ScriptAccess = "write"
)

type Script struct {
	ID                 string                    `json:"id"`
	WorkspaceID        string                    `json:"workspace_id,omitempty"`
	Name               string                    `json:"name"`
	Path               string                    `json:"path"`
	Description        string                    `json:"description,omitempty"`
	Enabled            bool                      `json:"enabled"`
	Access             ScriptAccess              `json:"access"`
	TimeoutSeconds     int64                     `json:"timeout_seconds,omitempty"`
	CredentialBindings []ScriptCredentialBinding `json:"secret_bindings"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`
}

// LocalApp describes a persistent workspace application definition. Command
// is an argv vector, never a shell expression. Runtime process state is kept
// outside SQLite and projected by the operator API.
type LocalApp struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Directory   string    `json:"directory"`
	Command     []string  `json:"command"`
	Port        int       `json:"port"`
	HealthPath  string    `json:"health_path,omitempty"`
	AutoStart   bool      `json:"auto_start"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type MCPTransport string

const (
	MCPTransportStdio MCPTransport = "stdio"
	MCPTransportHTTP  MCPTransport = "http"
)

// MCPAccess controls how Codex treats tools exposed by one server. Read mode
// allows tools annotated as read-only while write-capable tools still require
// approval. Full mode is an explicit owner opt-in for automatic tool use.
type MCPAccess string

const (
	MCPAccessRead MCPAccess = "read"
	MCPAccessFull MCPAccess = "full"
)

// MCPAuth describes how a Streamable HTTP server authenticates. OAuth is
// completed by Codex and stored in Codex's protected MCP credential store;
// secret bindings continue to use Nabu's workspace-scoped system vault.
type MCPAuth string

const (
	MCPAuthNone   MCPAuth = "none"
	MCPAuthOAuth  MCPAuth = "oauth"
	MCPAuthSecret MCPAuth = "secret"
)

// MCPSecretBinding maps one workspace vault entry to an environment variable
// consumed by Codex's MCP client. HTTP bindings can use that variable as a
// bearer token or a named request header. Runtime credential metadata is
// hydrated from SecretRecord and is never persisted or returned as JSON.
type MCPSecretBinding struct {
	SecretRecordID        string `json:"secret_id"`
	Env                   string `json:"env_var"`
	Header                string `json:"header,omitempty"`
	Bearer                bool   `json:"bearer,omitempty"`
	CredentialIntegration string `json:"-"`
	CredentialName        string `json:"-"`
}

// MCPServer stores workspace-scoped connection metadata only. Ready and
// MissingSecrets are derived at the API boundary; no secret values enter
// SQLite or JSON.
type MCPServer struct {
	ID                    string             `json:"id"`
	WorkspaceID           string             `json:"workspace_id,omitempty"`
	Name                  string             `json:"name"`
	Description           string             `json:"description,omitempty"`
	Transport             MCPTransport       `json:"transport"`
	Command               string             `json:"command,omitempty"`
	Args                  []string           `json:"args,omitempty"`
	URL                   string             `json:"url,omitempty"`
	Auth                  MCPAuth            `json:"auth"`
	Enabled               bool               `json:"enabled"`
	Access                MCPAccess          `json:"access"`
	Required              bool               `json:"required"`
	StartupTimeoutSeconds int64              `json:"startup_timeout_seconds,omitempty"`
	ToolTimeoutSeconds    int64              `json:"tool_timeout_seconds,omitempty"`
	EnabledTools          []string           `json:"enabled_tools,omitempty"`
	DisabledTools         []string           `json:"disabled_tools,omitempty"`
	SecretBindings        []MCPSecretBinding `json:"secret_bindings"`
	Ready                 bool               `json:"ready"`
	AuthStatus            string             `json:"auth_status,omitempty"`
	MissingSecrets        []string           `json:"missing_secrets,omitempty"`
	CreatedAt             time.Time          `json:"created_at"`
	UpdatedAt             time.Time          `json:"updated_at"`
}

type ScriptRunStatus string

const (
	ScriptRunPending     ScriptRunStatus = "pending"
	ScriptRunRunning     ScriptRunStatus = "running"
	ScriptRunCompleted   ScriptRunStatus = "completed"
	ScriptRunFailed      ScriptRunStatus = "failed"
	ScriptRunCancelled   ScriptRunStatus = "cancelled"
	ScriptRunTimedOut    ScriptRunStatus = "timed_out"
	ScriptRunInterrupted ScriptRunStatus = "interrupted"
)

type ScriptResult struct {
	Status      string          `json:"status"`
	Summary     string          `json:"summary"`
	Data        json.RawMessage `json:"data,omitempty"`
	Interesting bool            `json:"interesting"`
	Artifacts   []Artifact      `json:"artifacts,omitempty"`
}

type ScriptRun struct {
	ID         string          `json:"id"`
	ScriptID   string          `json:"script_id"`
	ScheduleID string          `json:"schedule_id,omitempty"`
	Status     ScriptRunStatus `json:"status"`
	PID        int             `json:"pid,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	EndedAt    *time.Time      `json:"ended_at,omitempty"`
	ExitCode   *int            `json:"exit_code,omitempty"`
	StdoutPath string          `json:"stdout_path,omitempty"`
	StderrPath string          `json:"stderr_path,omitempty"`
	Result     *ScriptResult   `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
}

type MemoryTarget string

const (
	MemoryDurable MemoryTarget = "durable"
	MemoryDaily   MemoryTarget = "daily"
	MemorySoul    MemoryTarget = "soul"
)

type MemoryUpdateStatus string

const (
	MemoryProposed MemoryUpdateStatus = "proposed"
	MemoryApplied  MemoryUpdateStatus = "applied"
	MemoryRejected MemoryUpdateStatus = "rejected"
)

// MemoryUpdate keeps proposed and applied context changes auditable in SQLite;
// Markdown remains a human-readable projection rather than operational truth.
type MemoryUpdate struct {
	ID            string             `json:"id"`
	WorkspaceID   string             `json:"workspace_id,omitempty"`
	Target        MemoryTarget       `json:"target"`
	Content       string             `json:"content"`
	Source        string             `json:"source,omitempty"`
	Status        MemoryUpdateStatus `json:"status"`
	RejectionNote string             `json:"rejection_note,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	ResolvedAt    *time.Time         `json:"resolved_at,omitempty"`
}

type IntegrationStatus string

const (
	IntegrationDraft            IntegrationStatus = "draft"
	IntegrationNeedsCredentials IntegrationStatus = "needs_credentials"
	IntegrationVerifying        IntegrationStatus = "verifying"
	IntegrationReady            IntegrationStatus = "ready"
	IntegrationFailed           IntegrationStatus = "failed"
	IntegrationDisabled         IntegrationStatus = "disabled"
)

// CredentialRequirement intentionally contains descriptive metadata only.
// Credential values belong in the platform credential store, never SQLite.
type CredentialRequirement struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
}

// Integration describes a provider-neutral generated adapter. Manifest is
// the adapter's versionable contract; secrets are deliberately excluded.
type Integration struct {
	ID                     string                  `json:"id"`
	WorkspaceID            string                  `json:"workspace_id,omitempty"`
	Name                   string                  `json:"name"`
	Provider               string                  `json:"provider"`
	Description            string                  `json:"description,omitempty"`
	Status                 IntegrationStatus       `json:"status"`
	Manifest               json.RawMessage         `json:"manifest"`
	CredentialRequirements []CredentialRequirement `json:"credential_requirements"`
	AllowedHosts           []string                `json:"allowed_hosts"`
	LastVerifiedAt         *time.Time              `json:"last_verified_at,omitempty"`
	LastError              string                  `json:"last_error,omitempty"`
	CreatedAt              time.Time               `json:"created_at"`
	UpdatedAt              time.Time               `json:"updated_at"`
}

// Capability is the product-facing term for a generated Integration adapter.
// The alias keeps persistence and orchestration contracts provider-neutral.
type Capability = Integration

type PlanStatus string

const (
	PlanProposed  PlanStatus = "proposed"
	PlanActive    PlanStatus = "active"
	PlanCompleted PlanStatus = "completed"
	PlanArchived  PlanStatus = "archived"
)

type PlanItemKind string

const (
	PlanItemTask      PlanItemKind = "task"
	PlanItemSchedule  PlanItemKind = "schedule"
	PlanItemMilestone PlanItemKind = "milestone"
)

type PlanItemStatus string

const (
	PlanItemProposed PlanItemStatus = "proposed"
	PlanItemAccepted PlanItemStatus = "accepted"
	PlanItemSkipped  PlanItemStatus = "skipped"
)

type PlanItem struct {
	ID         string           `json:"id"`
	PlanID     string           `json:"plan_id"`
	Kind       PlanItemKind     `json:"kind"`
	Title      string           `json:"title"`
	Purpose    string           `json:"purpose,omitempty"`
	Why        string           `json:"why,omitempty"`
	PlannedAt  *time.Time       `json:"planned_at,omitempty"`
	Cadence    *ScheduleCadence `json:"cadence,omitempty"`
	Position   int              `json:"position"`
	TaskID     string           `json:"task_id,omitempty"`
	ScheduleID string           `json:"schedule_id,omitempty"`
	Status     PlanItemStatus   `json:"status"`
}

type Plan struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id,omitempty"`
	Title       string     `json:"title"`
	Objective   string     `json:"objective"`
	Status      PlanStatus `json:"status"`
	Items       []PlanItem `json:"items"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type DatasetColumnType string

const (
	DatasetString   DatasetColumnType = "string"
	DatasetInteger  DatasetColumnType = "integer"
	DatasetNumber   DatasetColumnType = "number"
	DatasetBoolean  DatasetColumnType = "boolean"
	DatasetDatetime DatasetColumnType = "datetime"
	DatasetJSON     DatasetColumnType = "json"
)

type DatasetColumn struct {
	Name        string            `json:"name"`
	Type        DatasetColumnType `json:"type"`
	Description string            `json:"description,omitempty"`
	Nullable    bool              `json:"nullable"`
}

// Dataset is workspace-scoped research/user data metadata. Rows live in a
// separate generic row store rather than Nabu's operational tables.
type Dataset struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	Description string          `json:"description,omitempty"`
	Schema      []DatasetColumn `json:"schema"`
	UniqueKey   []string        `json:"unique_key"`
	RowCount    int64           `json:"row_count"`
	DeletedAt   *time.Time      `json:"deleted_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type DatasetRow struct {
	ID        int64          `json:"id"`
	Values    map[string]any `json:"values"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type OrientationTask struct {
	Title            string           `json:"title"`
	Purpose          string           `json:"purpose"`
	Why              string           `json:"why"`
	Priority         Priority         `json:"priority"`
	DefinitionOfDone []DefinitionItem `json:"definition_of_done"`
	WorkspaceID      string           `json:"workspace_id,omitempty"`
}

type PriorityUpdate struct {
	TaskID   string   `json:"task_id"`
	Priority Priority `json:"priority"`
}

type OrientationResult struct {
	Summary         string            `json:"summary"`
	Tasks           []OrientationTask `json:"tasks"`
	PriorityUpdates []PriorityUpdate  `json:"priority_updates"`
	NoWorkNeeded    bool              `json:"no_work_needed"`
}

// OperatorActivity describes work the daemon is actively executing or has
// durably queued. It intentionally carries identifiers and labels only: task
// prompts, chat content, and other potentially sensitive payloads stay in
// their dedicated APIs.
type OperatorActivity struct {
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	EntityID string `json:"entity_id,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type StatusSnapshot struct {
	Status            GlobalStatus       `json:"status"`
	DisplayName       string             `json:"display_name"`
	SetupComplete     bool               `json:"setup_complete"`
	MissionStarted    bool               `json:"mission_started"`
	ContextReady      bool               `json:"context_ready"`
	Paused            bool               `json:"paused"`
	CodexAvailable    bool               `json:"codex_available"`
	CodexState        string             `json:"codex_state"`
	CodexReason       string             `json:"codex_reason,omitempty"`
	CodexRetryAt      *time.Time         `json:"codex_retry_at,omitempty"`
	ServiceHealthy    bool               `json:"service_healthy"`
	DiskFreeBytes     uint64             `json:"disk_free_bytes"`
	LastBackupAt      *time.Time         `json:"last_backup_at,omitempty"`
	Version           string             `json:"version"`
	ActiveTask        *Task              `json:"active_task,omitempty"`
	Activities        []OperatorActivity `json:"activities"`
	ChatQueued        int                `json:"chat_queued"`
	ReadyCount        int                `json:"ready_count"`
	NeedsAttention    int                `json:"needs_attention"`
	NextOrientationAt *time.Time         `json:"next_orientation_at,omitempty"`
}
