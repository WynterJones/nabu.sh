package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

type ScopeCreate struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Mission string `json:"mission,omitempty"`
	Context string `json:"context,omitempty"`
}

type ScopeUpdate struct {
	Name *string `json:"name,omitempty"`
	Path *string `json:"path,omitempty"`
}

type ScopeDelete struct {
	Confirmation string `json:"confirmation"`
}

type ScopeDeleteResult struct {
	DeletedWorkspaceID string `json:"deleted_workspace_id"`
	ActiveWorkspaceID  string `json:"active_workspace_id,omitempty"`
	FolderPreserved    bool   `json:"folder_preserved"`
}

type ChatPageRequest struct {
	BeforeID     int64
	Limit        int
	ThreadRootID int64
}

type ChatPage struct {
	Messages     []domain.Message `json:"messages"`
	HasMore      bool             `json:"has_more"`
	NextBeforeID int64            `json:"next_before_id,omitempty"`
}

type ChatSend struct {
	Content           string `json:"content"`
	ParentMessageID   int64  `json:"parent_message_id,omitempty"`
	RecoveryTaskID    string `json:"-"`
	RecoveryTaskTitle string `json:"-"`
}

type ScheduleInput struct {
	Name            *string               `json:"name,omitempty"`
	Enabled         *bool                 `json:"enabled,omitempty"`
	Kind            *domain.ScheduleKind  `json:"kind,omitempty"`
	Expression      *string               `json:"expression,omitempty"`
	IntervalSeconds *int64                `json:"interval_seconds,omitempty"`
	Payload         json.RawMessage       `json:"payload,omitempty"`
	Cadence         *ScheduleCadenceInput `json:"cadence,omitempty"`
}

type ScheduleCadenceInput struct {
	Expression      *string `json:"expression,omitempty"`
	IntervalSeconds *int64  `json:"interval_seconds,omitempty"`
}

type ScriptInput struct {
	Name           *string                           `json:"name,omitempty"`
	Path           *string                           `json:"path,omitempty"`
	Description    *string                           `json:"description,omitempty"`
	Enabled        *bool                             `json:"enabled,omitempty"`
	Access         *domain.ScriptAccess              `json:"access,omitempty"`
	TimeoutSeconds *int64                            `json:"timeout_seconds,omitempty"`
	SecretBindings *[]domain.ScriptCredentialBinding `json:"secret_bindings,omitempty"`
}

type MemoryView struct {
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type SoulView struct {
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type OperatorSettings struct {
	CodexModel           string `json:"codex_model,omitempty"`
	CodexReasoningEffort string `json:"codex_reasoning_effort,omitempty"`
	MaxParallelTasks     int    `json:"max_parallel_tasks"`
}

// ProductBackend is the phase 6–10 surface. Keeping it separate preserves the
// small phase 1–5 server contract for focused tests and alternative clients.
type ProductBackend interface {
	Scopes(context.Context) ([]domain.Workspace, error)
	ActiveScope(context.Context) (domain.Workspace, error)
	CreateScope(context.Context, ScopeCreate) (domain.Workspace, error)
	UpdateScope(context.Context, string, ScopeUpdate) (domain.Workspace, error)
	DeleteScope(context.Context, string, ScopeDelete) (ScopeDeleteResult, error)
	SetActiveScope(context.Context, string) (domain.Workspace, error)
	ChatMessages(context.Context, ChatPageRequest) (ChatPage, error)
	SendChat(context.Context, ChatSend) (domain.Message, error)
	DeleteChatMessage(context.Context, int64) error
	ChatWorking() bool
	ChatActive() bool
	ChatQueueDepth(context.Context) (int, error)
	Reports(context.Context) ([]domain.Report, error)
	Report(context.Context, string) (domain.Report, error)
	UpdateReportStatus(context.Context, string, domain.ReportStatus) (domain.Report, error)
	DeleteReport(context.Context, string) error
	Approvals(context.Context, []domain.ApprovalStatus) ([]domain.Approval, error)
	Approval(context.Context, string) (domain.Approval, error)
	ResolveApproval(context.Context, string, domain.ApprovalStatus, string) (domain.Approval, error)
	Policy(context.Context) (domain.Policy, error)
	UpdatePolicy(context.Context, domain.Policy) (domain.Policy, error)
	Schedules(context.Context) ([]domain.Schedule, error)
	CreateSchedule(context.Context, ScheduleInput) (domain.Schedule, error)
	UpdateSchedule(context.Context, string, ScheduleInput) (domain.Schedule, error)
	DeleteSchedule(context.Context, string) error
	Scripts(context.Context) ([]domain.Script, error)
	Script(context.Context, string) (domain.Script, error)
	CreateScript(context.Context, ScriptInput) (domain.Script, error)
	UpdateScript(context.Context, string, ScriptInput) (domain.Script, error)
	DeleteScript(context.Context, string) error
	RunScript(context.Context, string) (domain.ScriptRun, error)
	ScriptRuns(context.Context, string) ([]domain.ScriptRun, error)
	ScriptRun(context.Context, string) (domain.ScriptRun, error)
	Memory(context.Context) (MemoryView, error)
	UpdateMemory(context.Context, string) (MemoryView, error)
	Soul(context.Context) (SoulView, error)
	UpdateSoul(context.Context, string) (SoulView, error)
	MemoryUpdates(context.Context) ([]domain.MemoryUpdate, error)
	ResolveMemoryUpdate(context.Context, string, domain.MemoryUpdateStatus, string) (domain.MemoryUpdate, error)
	RestartService(context.Context) error
	OperatorSettings(context.Context) (OperatorSettings, error)
	UpdateOperatorSettings(context.Context, OperatorSettings) (OperatorSettings, error)
}

func (s *Server) registerProductRoutes(mux *http.ServeMux) {
	s.registerIntegrationRoutes(mux)
	s.registerSecretRoutes(mux)
	s.registerMCPRoutes(mux)
	mux.HandleFunc("GET /api/scopes", s.getScopes)
	mux.HandleFunc("POST /api/scopes", s.postScope)
	mux.HandleFunc("PATCH /api/scopes/{id}", s.patchScope)
	mux.HandleFunc("DELETE /api/scopes/{id}", s.deleteScope)
	mux.HandleFunc("GET /api/scopes/active", s.getActiveScope)
	mux.HandleFunc("POST /api/scopes/active", s.postActiveScope)
	mux.HandleFunc("GET /api/chat/messages", s.getChatMessages)
	mux.HandleFunc("POST /api/chat/messages", s.postChatMessage)
	mux.HandleFunc("GET /api/chat/messages/{id}/thread", s.getChatThread)
	mux.HandleFunc("POST /api/chat/messages/{id}/thread", s.postChatThread)
	mux.HandleFunc("DELETE /api/chat/messages/{id}", s.deleteChatMessage)
	mux.HandleFunc("GET /api/chat/status", s.getChatStatus)
	mux.HandleFunc("GET /api/reports", s.getReports)
	mux.HandleFunc("GET /api/reports/{id}", s.getReport)
	mux.HandleFunc("PATCH /api/reports/{id}", s.patchReport)
	mux.HandleFunc("DELETE /api/reports/{id}", s.deleteReport)
	mux.HandleFunc("GET /api/approvals", s.getApprovals)
	mux.HandleFunc("GET /api/approvals/{id}", s.getApproval)
	mux.HandleFunc("POST /api/approvals/{id}/resolve", s.postApprovalResolution)
	mux.HandleFunc("GET /api/policy", s.getPolicy)
	mux.HandleFunc("PUT /api/policy", s.putPolicy)
	mux.HandleFunc("GET /api/schedules", s.getSchedules)
	mux.HandleFunc("POST /api/schedules", s.postSchedule)
	mux.HandleFunc("PATCH /api/schedules/{id}", s.patchSchedule)
	mux.HandleFunc("DELETE /api/schedules/{id}", s.deleteSchedule)
	mux.HandleFunc("GET /api/scripts", s.getScripts)
	mux.HandleFunc("POST /api/scripts", s.postScript)
	mux.HandleFunc("GET /api/scripts/{id}", s.getScript)
	mux.HandleFunc("PATCH /api/scripts/{id}", s.patchScript)
	mux.HandleFunc("DELETE /api/scripts/{id}", s.deleteScript)
	mux.HandleFunc("POST /api/scripts/{id}/run", s.postScriptRun)
	mux.HandleFunc("GET /api/script-runs", s.getScriptRuns)
	mux.HandleFunc("GET /api/script-runs/{id}", s.getScriptRun)
	mux.HandleFunc("GET /api/memory", s.getMemory)
	mux.HandleFunc("PUT /api/memory", s.putMemory)
	mux.HandleFunc("GET /api/soul", s.getSoul)
	mux.HandleFunc("PUT /api/soul", s.putSoul)
	mux.HandleFunc("GET /api/memory/updates", s.getMemoryUpdates)
	mux.HandleFunc("POST /api/memory/updates/{id}/resolve", s.postMemoryResolution)
	mux.HandleFunc("GET /api/health", s.getHealth)
	mux.HandleFunc("POST /api/service/restart", s.postServiceRestart)
	mux.HandleFunc("GET /api/settings/operator", s.getOperatorSettings)
	mux.HandleFunc("PUT /api/settings/operator", s.putOperatorSettings)
}
func (s *Server) patchScope(w http.ResponseWriter, r *http.Request) {
	backend := s.product(w)
	if backend == nil {
		return
	}
	var input ScopeUpdate
	if !s.decode(w, r, &input) {
		return
	}
	if input.Name == nil && input.Path == nil {
		writeError(w, http.StatusBadRequest, "invalid_scope", "At least one scope field is required.")
		return
	}
	value, err := backend.UpdateScope(r.Context(), r.PathValue("id"), input)
	s.respond(w, value, err)
}

func (s *Server) deleteScope(w http.ResponseWriter, r *http.Request) {
	backend := s.product(w)
	if backend == nil {
		return
	}
	var input ScopeDelete
	if !s.decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Confirmation) == "" {
		writeError(w, http.StatusBadRequest, "invalid_scope", "Type the workspace name to confirm deletion.")
		return
	}
	value, err := backend.DeleteScope(r.Context(), strings.TrimSpace(r.PathValue("id")), input)
	s.respond(w, value, err)
}

func (s *Server) product(w http.ResponseWriter) ProductBackend {
	backend, ok := s.backend.(ProductBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "feature_unavailable", "This Nabu build does not include the requested feature.")
		return nil
	}
	return backend
}

func (s *Server) getScopes(w http.ResponseWriter, r *http.Request) {
	if backend := s.product(w); backend != nil {
		value, err := backend.Scopes(r.Context())
		s.respond(w, value, err)
	}
}
func (s *Server) getActiveScope(w http.ResponseWriter, r *http.Request) {
	if backend := s.product(w); backend != nil {
		value, err := backend.ActiveScope(r.Context())
		s.respond(w, value, err)
	}
}
func (s *Server) postScope(w http.ResponseWriter, r *http.Request) {
	backend := s.product(w)
	if backend == nil {
		return
	}
	var input ScopeCreate
	if !s.decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Path) == "" {
		writeError(w, http.StatusBadRequest, "invalid_scope", "Workspace name and path are required.")
		return
	}
	if strings.TrimSpace(input.Mission) == "" {
		writeError(w, http.StatusBadRequest, "invalid_scope", "A starting mission is required for a new workspace.")
		return
	}
	value, err := backend.CreateScope(r.Context(), input)
	if err == nil {
		writeJSON(w, http.StatusCreated, value)
		return
	}
	s.respond(w, nil, err)
}
func (s *Server) postActiveScope(w http.ResponseWriter, r *http.Request) {
	backend := s.product(w)
	if backend == nil {
		return
	}
	var input struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "invalid_scope", "workspace_id is required.")
		return
	}
	value, err := backend.SetActiveScope(r.Context(), input.WorkspaceID)
	s.respond(w, value, err)
}

func chatCursor(r *http.Request, defaultLimit, maximum int) (ChatPageRequest, error) {
	result := ChatPageRequest{Limit: defaultLimit}
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > maximum {
			return result, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalid, maximum)
		}
		result.Limit = parsed
	}
	if value := r.URL.Query().Get("before_id"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 1 {
			return result, fmt.Errorf("%w: before_id must be a positive integer", ErrInvalid)
		}
		result.BeforeID = parsed
	}
	return result, nil
}
func (s *Server) getChatMessages(w http.ResponseWriter, r *http.Request) {
	backend := s.product(w)
	if backend == nil {
		return
	}
	input, err := chatCursor(r, 10, 50)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	value, err := backend.ChatMessages(r.Context(), input)
	s.respond(w, value, err)
}
func parseMessageID(r *http.Request) (int64, error) {
	value, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("%w: invalid message ID", ErrInvalid)
	}
	return value, nil
}
func (s *Server) getChatThread(w http.ResponseWriter, r *http.Request) {
	backend := s.product(w)
	if backend == nil {
		return
	}
	rootID, err := parseMessageID(r)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	input, err := chatCursor(r, 20, 100)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	input.ThreadRootID = rootID
	value, err := backend.ChatMessages(r.Context(), input)
	s.respond(w, value, err)
}
func (s *Server) postChatMessage(w http.ResponseWriter, r *http.Request) { s.postChat(w, r, 0) }
func (s *Server) postChatThread(w http.ResponseWriter, r *http.Request) {
	id, err := parseMessageID(r)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	s.postChat(w, r, id)
}
func (s *Server) postChat(w http.ResponseWriter, r *http.Request, parentID int64) {
	backend := s.product(w)
	if backend == nil {
		return
	}
	var input ChatSend
	if !s.decode(w, r, &input) {
		return
	}
	input.ParentMessageID = parentID
	if strings.TrimSpace(input.Content) == "" {
		writeError(w, http.StatusBadRequest, "invalid_message", "Message cannot be empty.")
		return
	}
	value, err := backend.SendChat(r.Context(), input)
	if err == nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"message": value})
		return
	}
	s.respond(w, nil, err)
}
func (s *Server) deleteChatMessage(w http.ResponseWriter, r *http.Request) {
	backend := s.product(w)
	if backend == nil {
		return
	}
	id, err := parseMessageID(r)
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	if err := backend.DeleteChatMessage(r.Context(), id); err != nil {
		s.respond(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) getChatStatus(w http.ResponseWriter, r *http.Request) {
	if backend := s.product(w); backend != nil {
		queued, err := backend.ChatQueueDepth(r.Context())
		if err != nil {
			s.respond(w, nil, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"working": backend.ChatActive(), "queued": queued})
	}
}

func (s *Server) getReports(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.Reports(r.Context())
		s.respond(w, v, e)
	}
}
func (s *Server) getReport(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.Report(r.Context(), r.PathValue("id"))
		s.respond(w, v, e)
	}
}
func (s *Server) patchReport(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		var input struct {
			Status domain.ReportStatus `json:"status"`
		}
		if !s.decode(w, r, &input) {
			return
		}
		v, e := b.UpdateReportStatus(r.Context(), r.PathValue("id"), input.Status)
		s.respond(w, v, e)
	}
}
func (s *Server) deleteReport(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		if err := b.DeleteReport(r.Context(), r.PathValue("id")); err != nil {
			s.respond(w, nil, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func (s *Server) getApprovals(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		var statuses []domain.ApprovalStatus
		for _, value := range r.URL.Query()["status"] {
			for _, item := range strings.Split(value, ",") {
				if item != "" {
					statuses = append(statuses, domain.ApprovalStatus(item))
				}
			}
		}
		v, e := b.Approvals(r.Context(), statuses)
		s.respond(w, v, e)
	}
}
func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.Approval(r.Context(), r.PathValue("id"))
		s.respond(w, v, e)
	}
}
func (s *Server) postApprovalResolution(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	var input struct {
		Decision      domain.ApprovalStatus `json:"decision"`
		RejectionNote string                `json:"rejection_note,omitempty"`
		Note          string                `json:"note,omitempty"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Decision != domain.ApprovalApproved && input.Decision != domain.ApprovalRejected {
		writeError(w, http.StatusBadRequest, "invalid_decision", "Decision must be approved or rejected.")
		return
	}
	if input.RejectionNote == "" {
		input.RejectionNote = input.Note
	}
	v, e := b.ResolveApproval(r.Context(), r.PathValue("id"), input.Decision, input.RejectionNote)
	s.respond(w, v, e)
}
func (s *Server) getPolicy(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.Policy(r.Context())
		s.respond(w, v, e)
	}
}
func (s *Server) putPolicy(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	var input domain.Policy
	if !s.decode(w, r, &input) {
		return
	}
	v, e := b.UpdatePolicy(r.Context(), input)
	s.respond(w, v, e)
}

func (s *Server) getSchedules(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.Schedules(r.Context())
		s.respond(w, v, e)
	}
}
func (s *Server) postSchedule(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	var input ScheduleInput
	if !s.decode(w, r, &input) {
		return
	}
	v, e := b.CreateSchedule(r.Context(), input)
	if e == nil {
		writeJSON(w, http.StatusCreated, v)
		return
	}
	s.respond(w, nil, e)
}
func (s *Server) patchSchedule(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	var input ScheduleInput
	if !s.decode(w, r, &input) {
		return
	}
	v, e := b.UpdateSchedule(r.Context(), r.PathValue("id"), input)
	s.respond(w, v, e)
}
func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	e := b.DeleteSchedule(r.Context(), r.PathValue("id"))
	if e == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.respond(w, nil, e)
}
func (s *Server) getScripts(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.Scripts(r.Context())
		s.respond(w, v, e)
	}
}
func (s *Server) getScript(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.Script(r.Context(), r.PathValue("id"))
		s.respond(w, v, e)
	}
}
func (s *Server) postScript(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	var input ScriptInput
	if !s.decode(w, r, &input) {
		return
	}
	v, e := b.CreateScript(r.Context(), input)
	if e == nil {
		writeJSON(w, http.StatusCreated, v)
		return
	}
	s.respond(w, nil, e)
}
func (s *Server) patchScript(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	var input ScriptInput
	if !s.decode(w, r, &input) {
		return
	}
	v, e := b.UpdateScript(r.Context(), r.PathValue("id"), input)
	s.respond(w, v, e)
}
func (s *Server) deleteScript(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	e := b.DeleteScript(r.Context(), r.PathValue("id"))
	if e == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.respond(w, nil, e)
}
func (s *Server) postScriptRun(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	v, e := b.RunScript(r.Context(), r.PathValue("id"))
	if e == nil {
		writeJSON(w, http.StatusAccepted, v)
		return
	}
	s.respond(w, nil, e)
}
func (s *Server) getScriptRuns(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.ScriptRuns(r.Context(), r.URL.Query().Get("script_id"))
		s.respond(w, v, e)
	}
}
func (s *Server) getScriptRun(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.ScriptRun(r.Context(), r.PathValue("id"))
		s.respond(w, v, e)
	}
}
func (s *Server) getMemory(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.Memory(r.Context())
		s.respond(w, v, e)
	}
}
func (s *Server) putMemory(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	v, e := b.UpdateMemory(r.Context(), input.Content)
	s.respond(w, v, e)
}
func (s *Server) getSoul(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.Soul(r.Context())
		s.respond(w, v, e)
	}
}
func (s *Server) putSoul(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	v, e := b.UpdateSoul(r.Context(), input.Content)
	s.respond(w, v, e)
}
func (s *Server) getMemoryUpdates(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.MemoryUpdates(r.Context())
		s.respond(w, v, e)
	}
}
func (s *Server) postMemoryResolution(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	var input struct {
		Decision      domain.MemoryUpdateStatus `json:"decision"`
		RejectionNote string                    `json:"rejection_note,omitempty"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Decision != domain.MemoryApplied && input.Decision != domain.MemoryRejected {
		writeError(w, http.StatusBadRequest, "invalid_decision", "Decision must be applied or rejected.")
		return
	}
	v, e := b.ResolveMemoryUpdate(r.Context(), r.PathValue("id"), input.Decision, input.RejectionNote)
	s.respond(w, v, e)
}
func (s *Server) getHealth(w http.ResponseWriter, r *http.Request) {
	v, e := s.backend.Status(r.Context())
	s.respond(w, v, e)
}
func (s *Server) postServiceRestart(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		if e := b.RestartService(r.Context()); e != nil {
			s.respond(w, nil, e)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]bool{"restarting": true})
	}
}

func (s *Server) getOperatorSettings(w http.ResponseWriter, r *http.Request) {
	if b := s.product(w); b != nil {
		v, e := b.OperatorSettings(r.Context())
		s.respond(w, v, e)
	}
}

func (s *Server) putOperatorSettings(w http.ResponseWriter, r *http.Request) {
	b := s.product(w)
	if b == nil {
		return
	}
	var input OperatorSettings
	if !s.decode(w, r, &input) {
		return
	}
	v, e := b.UpdateOperatorSettings(r.Context(), input)
	s.respond(w, v, e)
}
