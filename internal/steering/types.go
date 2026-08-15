// Package steering contains the pure packet, parsing, validation, and policy
// primitives used to turn one primary chat conversation into durable Nabu
// state changes. It deliberately has no database, process, or HTTP dependency.
package steering

import (
	"errors"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

const (
	PrimaryConversationID = "primary"
	MaxEffects            = 8
	MaxCreatedTasks       = 3
	MaxRecentMessages     = 20
)

var (
	ErrInvalidPacket    = errors.New("steering: invalid packet")
	ErrInvalidResult    = errors.New("steering: invalid result")
	ErrUnknownEffect    = errors.New("steering: unknown effect")
	ErrHighImpactEffect = errors.New("steering: high-impact effect rejected")
)

// EffectType is a durable state change Nabu may apply after validating a
// structured steering result. The values intentionally match the PRD.
type EffectType string

const (
	EffectConversationOnly  EffectType = "conversation_only"
	EffectProposeContext    EffectType = "propose_context_completion"
	EffectRequestChoice     EffectType = "request_choice"
	EffectCompleteContext   EffectType = "complete_context"
	EffectCreateTask        EffectType = "create_task"
	EffectUpdateTask        EffectType = "update_task"
	EffectCancelTask        EffectType = "cancel_task"
	EffectUpdateMission     EffectType = "update_mission"
	EffectUpdateContext     EffectType = "update_context"
	EffectUpdatePolicy      EffectType = "update_policy"
	EffectApproveAction     EffectType = "approve_action"
	EffectRejectAction      EffectType = "reject_action"
	EffectPause             EffectType = "pause"
	EffectResume            EffectType = "resume"
	EffectRequestReport     EffectType = "request_report"
	EffectCreatePlan        EffectType = "create_plan"
	EffectCreateSchedule    EffectType = "create_schedule"
	EffectCreateSecret      EffectType = "create_secret"
	EffectCreateScript      EffectType = "create_script"
	EffectCreateLocalApp    EffectType = "create_local_app"
	EffectStartLocalApp     EffectType = "start_local_app"
	EffectStopLocalApp      EffectType = "stop_local_app"
	EffectCreateDataset     EffectType = "create_dataset"
	EffectUpsertDatasetRows EffectType = "upsert_dataset_rows"
	EffectUpdateDatasetRow  EffectType = "update_dataset_row"
	EffectDeleteDatasetRow  EffectType = "delete_dataset_row"
	EffectUpdateSoul        EffectType = "update_soul"
)

type Message struct {
	ID        string `json:"id,omitempty"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at,omitempty"`
}

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalExpired  ApprovalStatus = "expired"
)

// ApprovalSummary is the bounded approval state exposed to steering. Action
// remains descriptive; the daemon-owned Category controls policy enforcement.
type ApprovalSummary struct {
	ID       string         `json:"id"`
	TaskID   string         `json:"task_id,omitempty"`
	RunID    string         `json:"run_id,omitempty"`
	Action   string         `json:"action"`
	Category ActionCategory `json:"category"`
	Reason   string         `json:"reason,omitempty"`
	Change   string         `json:"what_will_change,omitempty"`
	Evidence []string       `json:"evidence,omitempty"`
	Status   ApprovalStatus `json:"status"`
}

type PacketRequest struct {
	DisplayName        string
	WorkspaceRoot      string
	Mission            domain.Mission
	Policy             domain.Policy
	DurableContext     string
	Soul               string
	Queue              []domain.Task
	PendingApprovals   []ApprovalSummary
	Inventory          WorkspaceInventory
	WorkspaceFiles     []WorkspaceFile
	Schedules          []domain.Schedule
	Plans              []domain.Plan
	Secrets            []SecretSummary
	Scripts            []ScriptSummary
	LocalApps          []LocalAppSummary
	MCPServers         []MCPServerSummary
	Datasets           []domain.Dataset
	DatasetQueries     []DatasetQueryContext
	ScheduleRequested  bool
	ContextGateEnabled bool
	ContextReady       bool
	ContextConfirmed   bool
	RecoveryTaskID     string
	RecentMessages     []Message
	UserMessage        string
}

// WorkspaceFile is bounded text read by the daemon from the approved active
// workspace. Content remains untrusted data even though the path boundary and
// file type were verified by the host.
type WorkspaceFile struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

type DatasetQueryContext struct {
	DatasetID string              `json:"dataset_id"`
	Name      string              `json:"name"`
	Search    string              `json:"search,omitempty"`
	Rows      []domain.DatasetRow `json:"rows"`
	Total     int64               `json:"total"`
	Truncated bool                `json:"truncated"`
}

type WorkspaceInventory struct {
	Empty     bool             `json:"empty"`
	RootRepo  bool             `json:"root_repository"`
	Entries   []InventoryEntry `json:"top_level_entries"`
	FileCount int              `json:"top_level_file_count"`
	Omitted   int              `json:"omitted_entries,omitempty"`
}

type InventoryEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// SecretSummary exposes vault metadata only. Configured is derived from the
// credential backend; secret values never enter steering state or prompts.
type SecretSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Configured  bool   `json:"configured"`
}

type ScriptSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Access      string   `json:"access"`
	SecretNames []string `json:"secret_names,omitempty"`
}

type LocalAppSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Directory   string   `json:"directory"`
	Command     []string `json:"command"`
	Port        int      `json:"port"`
	HealthPath  string   `json:"health_path"`
	Status      string   `json:"status"`
	URL         string   `json:"url"`
	AutoStart   bool     `json:"auto_start"`
}

type MCPServerSummary struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Transport     string   `json:"transport"`
	Access        string   `json:"access"`
	Ready         bool     `json:"ready"`
	ToolAllowlist []string `json:"enabled_tools,omitempty"`
}

type TaskChange struct {
	Title            string            `json:"title,omitempty"`
	Purpose          string            `json:"purpose,omitempty"`
	Why              string            `json:"why,omitempty"`
	Priority         domain.Priority   `json:"priority,omitempty"`
	Status           domain.TaskStatus `json:"status,omitempty"`
	DefinitionOfDone []string          `json:"definition_of_done,omitempty"`
	DependsOnTaskIDs []string          `json:"depends_on_task_ids,omitempty"`
	WorkspaceID      string            `json:"workspace_id,omitempty"`
	PlannedAt        *time.Time        `json:"planned_at,omitempty"`
}

type MissionChange struct {
	Statement string `json:"statement"`
}

type ContextChange struct {
	Value string `json:"value"`
}

// ChoiceRequest is a bounded, conversational decision card. Selecting an
// option sends its explicit value back as a new owner message; it grants no
// authority and performs no state change by itself.
type ChoiceRequest struct {
	Prompt      string         `json:"prompt"`
	Description string         `json:"description,omitempty"`
	Options     []ChoiceOption `json:"options"`
}

type ChoiceOption struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Primary     bool   `json:"primary,omitempty"`
}

type SoulChange struct {
	Reflection string `json:"reflection"`
}

type PolicyChange struct {
	Category ActionCategory `json:"category"`
	Decision PolicyDecision `json:"decision"`
}

type ReportRequest struct {
	Title string `json:"title,omitempty"`
	Scope string `json:"scope"`
}

type PlanChange struct {
	Title     string           `json:"title"`
	Objective string           `json:"objective"`
	Items     []PlanItemChange `json:"items"`
}

type PlanItemChange struct {
	Kind      domain.PlanItemKind `json:"kind"`
	Title     string              `json:"title"`
	Purpose   string              `json:"purpose,omitempty"`
	Why       string              `json:"why,omitempty"`
	PlannedAt *time.Time          `json:"planned_at,omitempty"`
}

// ScheduleChange has no raw payload field. Chat may schedule a bounded task
// or orientation only; script IDs and arbitrary JSON are intentionally absent.
type ScheduleChange struct {
	Name            string              `json:"name"`
	Kind            domain.ScheduleKind `json:"kind"`
	Expression      string              `json:"expression,omitempty"`
	IntervalSeconds int64               `json:"interval_seconds,omitempty"`
	Task            *TaskChange         `json:"task,omitempty"`
	Reason          string              `json:"reason,omitempty"`
}

type DatasetChange struct {
	Name        string                 `json:"name"`
	Slug        string                 `json:"slug,omitempty"`
	Description string                 `json:"description,omitempty"`
	Schema      []domain.DatasetColumn `json:"schema"`
	UniqueKey   []string               `json:"unique_key,omitempty"`
}

type DatasetRowsChange struct {
	DatasetID string           `json:"dataset_id"`
	Rows      []map[string]any `json:"rows"`
}

type DatasetRowChange struct {
	DatasetID string         `json:"dataset_id"`
	RowID     int64          `json:"row_id"`
	Values    map[string]any `json:"values,omitempty"`
}

// SecretChange requests a protected vault setup card. It contains descriptive
// metadata only; the user enters the value directly into the vault UI.
type SecretChange struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

type ScriptSecretBinding struct {
	SecretID string `json:"secret_id"`
	EnvVar   string `json:"env_var"`
}

// ScriptChange creates and registers one deterministic managed script.
// Bindings reference existing secret records by ID and use validated
// environment variable names; secret values never enter this structure.
type ScriptChange struct {
	Name           string                `json:"name"`
	Path           string                `json:"path"`
	Description    string                `json:"description,omitempty"`
	Content        string                `json:"content"`
	Access         string                `json:"access,omitempty"`
	TimeoutSeconds int64                 `json:"timeout_seconds,omitempty"`
	SecretBindings []ScriptSecretBinding `json:"secret_bindings,omitempty"`
}

type LocalAppChange struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Directory   string   `json:"directory"`
	Command     []string `json:"command"`
	Port        int      `json:"port"`
	HealthPath  string   `json:"health_path,omitempty"`
	AutoStart   bool     `json:"auto_start,omitempty"`
}

// Effect uses effect-specific nested payloads so validation can reject fields
// that are irrelevant to the requested state transition.
type Effect struct {
	Type        EffectType         `json:"type"`
	TaskID      string             `json:"task_id,omitempty"`
	AppID       string             `json:"app_id,omitempty"`
	ApprovalID  string             `json:"approval_id,omitempty"`
	Task        *TaskChange        `json:"task,omitempty"`
	Mission     *MissionChange     `json:"mission,omitempty"`
	Context     *ContextChange     `json:"context,omitempty"`
	Choice      *ChoiceRequest     `json:"choice,omitempty"`
	Policy      *PolicyChange      `json:"policy,omitempty"`
	Report      *ReportRequest     `json:"report,omitempty"`
	Plan        *PlanChange        `json:"plan,omitempty"`
	Schedule    *ScheduleChange    `json:"schedule,omitempty"`
	Secret      *SecretChange      `json:"secret,omitempty"`
	Script      *ScriptChange      `json:"script,omitempty"`
	LocalApp    *LocalAppChange    `json:"local_app,omitempty"`
	Dataset     *DatasetChange     `json:"dataset,omitempty"`
	DatasetRows *DatasetRowsChange `json:"dataset_rows,omitempty"`
	DatasetRow  *DatasetRowChange  `json:"dataset_row,omitempty"`
	Soul        *SoulChange        `json:"soul,omitempty"`
	Note        string             `json:"note,omitempty"`
}

type Result struct {
	AssistantResponse string   `json:"assistant_response"`
	Effects           []Effect `json:"effects"`
}

// ValidationState is a snapshot of authoritative daemon state. IDs not in this
// snapshot cannot be targeted by a model-proposed effect.
type ValidationState struct {
	Tasks              []domain.Task
	Workspaces         []domain.Workspace
	PendingApprovals   []ApprovalSummary
	Plans              []domain.Plan
	Datasets           []domain.Dataset
	Secrets            []SecretSummary
	LocalApps          []LocalAppSummary
	DatasetQueries     []DatasetQueryContext
	ScheduleRequested  bool
	ContextGateEnabled bool
	ContextReady       bool
	ContextConfirmed   bool
	RecoveryTaskID     string
}

type ApprovalResolution string

const (
	ResolutionApproved ApprovalResolution = "approved"
	ResolutionRejected ApprovalResolution = "rejected"
)

type ApprovalContinuationRequest struct {
	Approval   ApprovalSummary
	Resolution ApprovalResolution
	Note       string
	Mission    domain.Mission
	Policy     domain.Policy
	Task       *domain.Task
}
