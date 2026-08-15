package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestWorkspaceScopesMissionsAndMissionState(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	w1, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w1", Name: "One", Path: "/one", Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w2", Name: "Two", Path: "/two", Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.ActiveWorkspace(ctx)
	if err != nil || active.ID != w1.ID {
		t.Fatalf("first active workspace = %+v, %v", active, err)
	}
	if _, err := s.CreateMission(ctx, domain.Mission{ID: "m1", Statement: "Mission one", Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveWorkspace(ctx, w2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMission(ctx, domain.Mission{ID: "m2", Statement: "Mission two", Active: true}); err != nil {
		t.Fatal(err)
	}
	m1, err := s.GetMissionForWorkspace(ctx, w1.ID)
	if err != nil || m1.ID != "m1" || m1.WorkspaceID != w1.ID {
		t.Fatalf("mission one = %+v, %v", m1, err)
	}
	m2, err := s.ActiveMission(ctx)
	if err != nil || m2.ID != "m2" || m2.WorkspaceID != w2.ID {
		t.Fatalf("active mission two = %+v, %v", m2, err)
	}
	w2.MissionStarted = true
	if err := s.UpdateWorkspace(ctx, w2); err != nil {
		t.Fatal(err)
	}
	settings, err := s.GetSettings(ctx)
	if err != nil || !settings.MissionStarted || settings.ActiveWorkspaceID != w2.ID {
		t.Fatalf("settings did not derive active mission state: %+v, %v", settings, err)
	}
	if err := s.SetActiveWorkspace(ctx, w1.ID); err != nil {
		t.Fatal(err)
	}
	settings, err = s.GetSettings(ctx)
	if err != nil || settings.MissionStarted {
		t.Fatalf("workspace mission state leaked: %+v, %v", settings, err)
	}
	if queued, err := s.RequestOrientationForWorkspace(ctx, w2.ID); err != nil || !queued {
		t.Fatalf("request workspace orientation = %v, %v", queued, err)
	}
	queued, at, err := s.OrientationStateForWorkspace(ctx, w2.ID)
	if err != nil || !queued || at != nil {
		t.Fatalf("workspace orientation state = %v, %v, %v", queued, at, err)
	}
	settings, err = s.GetSettings(ctx)
	if err != nil || settings.OrientationQueued {
		t.Fatalf("inactive workspace orientation leaked: %+v, %v", settings, err)
	}
	completed := time.Now().UTC()
	if err := s.CompleteOrientationForWorkspace(ctx, w2.ID, completed); err != nil {
		t.Fatal(err)
	}
	queued, at, err = s.OrientationStateForWorkspace(ctx, w2.ID)
	if err != nil || queued || at == nil || !at.Equal(completed) {
		t.Fatalf("completed workspace orientation = %v, %v, %v", queued, at, err)
	}
}

func TestMessageScopeChronologyPagingAndThreads(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w1", Name: "One", Path: "/one", ContextReady: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w2", Name: "Two", Path: "/two"}); err != nil {
		t.Fatal(err)
	}
	root, err := s.AppendMessage(ctx, domain.Message{
		WorkspaceID: "w1", Role: domain.MessageUser, Content: "root",
		Effect: domain.EffectCreateTask, EffectMetadata: json.RawMessage(`{"task_id":"t1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"reply 1", "reply 2", "reply 3"} {
		if _, err := s.AppendMessage(ctx, domain.Message{WorkspaceID: "w1", ParentMessageID: &root.ID, Role: domain.MessageAssistant, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	for _, content := range []string{"second root", "third root"} {
		if _, err := s.AppendMessage(ctx, domain.Message{WorkspaceID: "w1", Role: domain.MessageUser, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AppendMessage(ctx, domain.Message{WorkspaceID: "w2", Role: domain.MessageUser, Content: "other scope"}); err != nil {
		t.Fatal(err)
	}

	top, err := s.ListMessages(ctx, MessageFilter{WorkspaceID: "w1", TopLevelOnly: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].Content != "second root" || top[1].Content != "third root" {
		t.Fatalf("newest top-level page = %+v", top)
	}
	thread, err := s.ListMessages(ctx, MessageFilter{WorkspaceID: "w1", ThreadRootID: root.ID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(thread) != 3 || thread[0].ID != root.ID || thread[1].Content != "reply 2" || thread[2].Content != "reply 3" || thread[0].ReplyCount != 3 {
		t.Fatalf("thread = %+v", thread)
	}
	older, err := s.ListMessages(ctx, MessageFilter{WorkspaceID: "w1", ThreadRootID: root.ID, BeforeID: thread[1].ID, Limit: 2})
	if err != nil || len(older) != 2 || older[0].ID != root.ID || older[1].Content != "reply 1" {
		t.Fatalf("older thread page = %+v, %v", older, err)
	}
	count, err := s.CountThreadReplies(ctx, root.ID)
	if err != nil || count != 3 {
		t.Fatalf("reply count = %d, %v", count, err)
	}
	other, err := s.ListMessages(ctx, MessageFilter{WorkspaceID: "w2", Limit: 10})
	if err != nil || len(other) != 1 || other[0].Content != "other scope" {
		t.Fatalf("other scope = %+v, %v", other, err)
	}
	if _, err := s.AppendMessage(ctx, domain.Message{WorkspaceID: "w2", ParentMessageID: &root.ID, Role: domain.MessageUser, Content: "cross-scope reply"}); err == nil {
		t.Fatal("cross-workspace thread reply was accepted")
	}
}

func TestMessagesAcceptProtectedCapabilityEffects(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w1", Name: "One", Path: "/one", ContextReady: true}); err != nil {
		t.Fatal(err)
	}
	for _, effect := range []domain.ChatEffect{domain.EffectCreateSecret, domain.EffectCreateScript} {
		if _, err := s.AppendMessage(ctx, domain.Message{
			WorkspaceID: "w1",
			Role:        domain.MessageAssistant,
			Content:     "Created protected capability.",
			Effect:      effect,
		}); err != nil {
			t.Fatalf("append %q effect: %v", effect, err)
		}
	}
}

func TestDeleteMessageCascadesThreadRepliesOnly(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w1", Name: "One", Path: "/one", ContextReady: true}); err != nil {
		t.Fatal(err)
	}
	root, err := s.AppendMessage(ctx, domain.Message{WorkspaceID: "w1", Role: domain.MessageUser, Content: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage(ctx, domain.Message{WorkspaceID: "w1", ParentMessageID: &root.ID, Role: domain.MessageAssistant, Content: "reply"}); err != nil {
		t.Fatal(err)
	}
	keep, err := s.AppendMessage(ctx, domain.Message{WorkspaceID: "w1", Role: domain.MessageAssistant, Content: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMessage(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	messages, err := s.ListMessages(ctx, MessageFilter{WorkspaceID: "w1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != keep.ID {
		t.Fatalf("messages after thread deletion = %#v", messages)
	}
	if _, err := s.GetMessage(ctx, root.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted root lookup error = %v, want not found", err)
	}
}

func TestApprovalTransitionsAreAtomicWithTask(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w1", Name: "One", Path: "/one"}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, domain.Task{ID: "t1", Title: "Publish", Status: domain.TaskRunning, Priority: domain.PriorityHigh})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	approval, err := s.CreateApproval(ctx, domain.Approval{
		ID: "a1", TaskID: task.ID, ProposedAction: "Publish page", Why: "Mission impact",
		ProposedChange: "Deploy one page", Evidence: "Tests pass", ExpiresAt: &expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	if approval.WorkspaceID != "w1" {
		t.Fatalf("approval scope = %q", approval.WorkspaceID)
	}
	pausedTask, err := s.GetTask(ctx, task.ID)
	if err != nil || pausedTask.Status != domain.TaskNeedsApproval {
		t.Fatalf("task after approval = %+v, %v", pausedTask, err)
	}
	resolvedAt := time.Now().UTC()
	resolved, err := s.ResolveApproval(ctx, approval.ID, domain.ApprovalRejected, "Revise copy", resolvedAt)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != domain.ApprovalRejected || resolved.RejectionNote != "Revise copy" || resolved.ResolvedAt == nil {
		t.Fatalf("resolved approval = %+v", resolved)
	}
	waitingTask, err := s.GetTask(ctx, task.ID)
	if err != nil || waitingTask.Status != domain.TaskWaiting {
		t.Fatalf("task after rejection = %+v, %v", waitingTask, err)
	}
	if _, err := s.ResolveApproval(ctx, approval.ID, domain.ApprovalApproved, "", resolvedAt); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second resolution = %v", err)
	}

	if err := s.UpdateTaskStatus(ctx, task.ID, domain.TaskRunning, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	a2, err := s.CreateApproval(ctx, domain.Approval{ID: "a2", TaskID: task.ID, ProposedAction: "Publish"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveApproval(ctx, a2.ID, domain.ApprovalApproved, "", resolvedAt); err != nil {
		t.Fatal(err)
	}
	readyTask, err := s.GetTask(ctx, task.ID)
	if err != nil || readyTask.Status != domain.TaskReady {
		t.Fatalf("task after approval = %+v, %v", readyTask, err)
	}

	past := resolvedAt.Add(-time.Minute)
	a3, err := s.CreateApproval(ctx, domain.Approval{ID: "a3", TaskID: task.ID, ProposedAction: "Publish", ExpiresAt: &past})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := s.ExpireApprovals(ctx, resolvedAt)
	if err != nil || expired != 1 {
		t.Fatalf("ExpireApprovals = %d, %v", expired, err)
	}
	a3, err = s.GetApproval(ctx, a3.ID)
	if err != nil || a3.Status != domain.ApprovalExpired {
		t.Fatalf("expired approval = %+v, %v", a3, err)
	}
}

func TestReportRelationsAndWorkspaceFiltering(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w1", Name: "One", Path: "/one"}); err != nil {
		t.Fatal(err)
	}
	t1, err := s.CreateTask(ctx, domain.Task{ID: "t1", Title: "Research", Status: domain.TaskCompleted, Priority: domain.PriorityNormal})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.CreateTask(ctx, domain.Task{ID: "t2", Title: "Review", Status: domain.TaskCompleted, Priority: domain.PriorityNormal})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := s.CreateArtifact(ctx, domain.Artifact{ID: "art1", TaskID: t1.ID, Kind: "screenshot", Name: "Preview"})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Report{
		ID: "report1", Kind: "research", Title: "Findings", Summary: "Concise",
		Body: "# Findings\n\nEvidence.", RelatedTaskIDs: []string{t2.ID, t1.ID, t1.ID}, ArtifactIDs: []string{artifact.ID},
	}
	created, err := s.CreateReport(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkspaceID != "w1" || len(created.RelatedTaskIDs) != 2 {
		t.Fatalf("created report = %+v", created)
	}
	got, err := s.GetReport(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != want.Title || !reflect.DeepEqual(got.RelatedTaskIDs, []string{"t1", "t2"}) || len(got.Artifacts) != 1 || got.Artifacts[0].ID != artifact.ID {
		t.Fatalf("report roundtrip = %+v", got)
	}
	list, err := s.ListReports(ctx, ReportFilter{WorkspaceID: "w1", TaskID: t2.ID})
	if err != nil || len(list) != 1 || list[0].ID != want.ID {
		t.Fatalf("reports for task = %+v, %v", list, err)
	}
}

func TestScheduleAtomicClaimLeaseRecoveryAndFinish(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nabu.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w1", Name: "One", Path: "/one", ContextReady: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	schedule, err := s.CreateSchedule(ctx, domain.Schedule{
		ID: "s1", Name: "Health", Enabled: true, Kind: domain.ScheduleScript,
		IntervalSeconds: 3600, Payload: json.RawMessage(`{"script_id":"health"}`), NextRunAt: &due,
	})
	if err != nil {
		t.Fatal(err)
	}
	var claims atomic.Int32
	var claim domain.Schedule
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.ClaimDueSchedule(ctx, now, time.Minute)
			if err == nil {
				claims.Add(1)
				mu.Lock()
				claim = got
				mu.Unlock()
			} else if !errors.Is(err, ErrNotFound) {
				t.Errorf("ClaimDueSchedule: %v", err)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 || claim.ID != schedule.ID || claim.ClaimToken == "" {
		t.Fatalf("claims = %d, claim = %+v", claims.Load(), claim)
	}
	if err := s.FinishScheduleClaim(ctx, claim.ID, "stale", nil, "", now); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("stale finish = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A new daemon can reclaim after the persisted lease expires.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	reclaimed, err := s.ClaimDueSchedule(ctx, now.Add(2*time.Minute), time.Minute)
	if err != nil || reclaimed.ID != schedule.ID || reclaimed.ClaimToken == claim.ClaimToken {
		t.Fatalf("reclaimed = %+v, %v", reclaimed, err)
	}
	next := now.Add(time.Hour)
	if err := s.FinishScheduleClaim(ctx, reclaimed.ID, reclaimed.ClaimToken, &next, "", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	finished, err := s.GetSchedule(ctx, schedule.ID)
	if err != nil || finished.ClaimToken != "" || finished.LastRunAt == nil || !finished.NextRunAt.Equal(next) {
		t.Fatalf("finished schedule = %+v, %v", finished, err)
	}
}

func TestScheduleClaimWaitsForWorkspaceContext(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "context-workspace", Name: "Context", Path: t.TempDir(), Allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	due := now.Add(-time.Minute)
	schedule, err := s.CreateSchedule(ctx, domain.Schedule{WorkspaceID: workspace.ID, Name: "Deferred", Enabled: true, Kind: domain.ScheduleOrient, IntervalSeconds: 3600, NextRunAt: &due})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDueSchedule(ctx, now, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unready workspace claim = %v, want not found", err)
	}
	workspace.ContextReady = true
	if err := s.UpdateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimDueSchedule(ctx, now, time.Minute)
	if err != nil || claimed.ID != schedule.ID {
		t.Fatalf("ready workspace claim = %#v, %v", claimed, err)
	}
}

func TestScriptsArtifactsMemoryAndRecovery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "w1", Name: "One", Path: "/one"}); err != nil {
		t.Fatal(err)
	}
	script, err := s.CreateScript(ctx, domain.Script{ID: "script1", Name: "health", Path: "/scripts/health", Enabled: true, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateScriptRun(ctx, domain.ScriptRun{ID: "sr1", ScriptID: script.ID, Status: domain.ScriptRunRunning, PID: 99})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := s.CreateArtifact(ctx, domain.Artifact{ID: "sa1", ScriptRunID: run.ID, Kind: "json", Name: "Result"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := s.ListScriptRunArtifacts(ctx, run.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].ID != artifact.ID || artifacts[0].ScriptRunID != run.ID {
		t.Fatalf("script artifacts = %+v, %v", artifacts, err)
	}
	update, err := s.CreateMemoryUpdate(ctx, domain.MemoryUpdate{ID: "mem1", Target: domain.MemoryDurable, Content: "Remember this", Source: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	update, err = s.ResolveMemoryUpdate(ctx, update.ID, domain.MemoryApplied, "", time.Now().UTC())
	if err != nil || update.Status != domain.MemoryApplied || update.ResolvedAt == nil {
		t.Fatalf("memory update = %+v, %v", update, err)
	}
	if _, err := s.ResolveMemoryUpdate(ctx, update.ID, domain.MemoryRejected, "", time.Now().UTC()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second memory resolution = %v", err)
	}
	recovery, err := s.RecoverInterrupted(ctx)
	if err != nil || recovery.ScriptRunsInterrupted != 1 {
		t.Fatalf("recovery = %+v, %v", recovery, err)
	}
	run, err = s.GetScriptRun(ctx, run.ID)
	if err != nil || run.Status != domain.ScriptRunInterrupted || run.PID != 0 || run.EndedAt == nil {
		t.Fatalf("recovered script run = %+v, %v", run, err)
	}
}

func TestBackupIntegrityAndData(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.AppendMessage(ctx, domain.Message{Role: domain.MessageUser, Content: "preserved"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backups", "nabu.db")
	if err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	settings, err := s.GetSettings(ctx)
	if err != nil || settings.LastBackupAt == nil {
		t.Fatalf("last backup state = %+v, %v", settings, err)
	}
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup info = size %d mode %v", info.Size(), info.Mode())
	}
	copy, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	messages, err := copy.ListMessages(ctx, MessageFilter{Limit: 10})
	if err != nil || len(messages) != 1 || messages[0].Content != "preserved" {
		t.Fatalf("backup messages = %+v, %v", messages, err)
	}
	if err := s.Backup(ctx, backup); err == nil {
		t.Fatal("Backup overwrote an existing destination")
	}
}

func TestMigrateLegacyPhaseOneDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:3] {
		if _, err := db.ExecContext(ctx, migration.up); err != nil {
			t.Fatalf("seed migration %d: %v", migration.version, err)
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (?, ?)", migration.version, formatTime(time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO workspaces(id,name,path,allowed,created_at) VALUES ('w1','Legacy','/legacy',1,?)`, formatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO messages(role,content,created_at) VALUES ('user','legacy message',?)`, formatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	version, err := s.SchemaVersion(ctx)
	if err != nil || version != len(migrations) {
		t.Fatalf("migrated version = %d, %v", version, err)
	}
	messages, err := s.ListMessages(ctx, MessageFilter{WorkspaceID: "w1", Limit: 10})
	if err != nil || len(messages) != 1 || messages[0].Content != "legacy message" || messages[0].WorkspaceID != "w1" {
		t.Fatalf("legacy messages = %+v, %v", messages, err)
	}
	chatRun, err := s.CreateRun(ctx, domain.Run{ID: "chat1", Type: domain.RunTypeChat, Status: domain.RunPending})
	if err != nil || chatRun.Type != domain.RunChat {
		t.Fatalf("chat run = %+v, %v", chatRun, err)
	}
}
