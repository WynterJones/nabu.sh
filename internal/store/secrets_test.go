package store

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestSecretRecordsAreMetadataOnlyAndWorkspaceScoped(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	w1, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "secret-w1", Name: "One", Path: "/secret-one"})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "secret-w2", Name: "Two", Path: "/secret-two"})
	if err != nil {
		t.Fatal(err)
	}

	created, err := s.CreateSecretRecord(ctx, domain.SecretRecord{
		ID:          "secret-record-one",
		WorkspaceID: w1.ID,
		Name:        "posthog-api-key",
		Description: "Read-only analytics token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ReferenceKey != created.ID || created.Label != created.Name {
		t.Fatalf("generated metadata = %#v", created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("timestamps were not assigned: %#v", created)
	}
	if _, err := s.CreateSecretRecord(ctx, domain.SecretRecord{
		WorkspaceID: w1.ID, Name: "bad-reference", ReferenceKey: "../../escape",
	}); err == nil {
		t.Fatal("unsafe credential reference was accepted")
	}

	got, err := s.GetSecretRecord(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, created) {
		t.Fatalf("GetSecretRecord = %#v, want %#v", got, created)
	}
	searched, err := s.ListSecretRecords(ctx, SecretRecordFilter{Search: "ANALYTICS"})
	if err != nil || len(searched) != 1 || searched[0].ID != created.ID {
		t.Fatalf("search = %#v, %v", searched, err)
	}

	changed := created
	changed.ReferenceKey = "replacement"
	changed.Description = "must not persist"
	if err := s.UpdateSecretRecord(ctx, changed); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable reference update error = %v", err)
	}
	got, err = s.GetSecretRecord(ctx, created.ID)
	if err != nil || got.Description != created.Description {
		t.Fatalf("failed update was not atomic: %#v, %v", got, err)
	}
	changed.ReferenceKey = created.ReferenceKey
	changed.Label = "PostHog key"
	changed.Description = "Rotated in the secure backend"
	if err := s.UpdateSecretRecord(ctx, changed); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSecretRecordByReferenceKeyForWorkspace(ctx, w1.ID, created.ReferenceKey)
	if err != nil || got.Label != changed.Label || got.Description != changed.Description {
		t.Fatalf("updated record = %#v, %v", got, err)
	}

	if err := s.SetActiveWorkspace(ctx, w2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSecretRecord(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace GetSecretRecord error = %v", err)
	}
	if err := s.DeleteSecretRecord(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace DeleteSecretRecord error = %v", err)
	}
	other, err := s.CreateSecretRecord(ctx, domain.SecretRecord{
		WorkspaceID: w2.ID, Name: created.Name, ReferenceKey: created.ReferenceKey,
	})
	if err != nil {
		t.Fatalf("workspace-scoped uniqueness rejected same metadata: %v", err)
	}
	if other.WorkspaceID != w2.ID {
		t.Fatalf("created in workspace %q, want %q", other.WorkspaceID, w2.ID)
	}

	var columns []string
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(secret_records)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(columns)
	wantColumns := []string{"created_at", "description", "id", "label", "name", "reference_key", "updated_at", "workspace_id"}
	if !reflect.DeepEqual(columns, wantColumns) {
		t.Fatalf("secret_records columns = %v, want metadata-only %v", columns, wantColumns)
	}
}

func TestScriptCredentialBindingsAreHydratedScopedAndAtomic(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	w1, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "binding-w1", Name: "One", Path: "/binding-one"})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "binding-w2", Name: "Two", Path: "/binding-two"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateSecretRecord(ctx, domain.SecretRecord{
		ID: "first-secret", WorkspaceID: w1.ID, Name: "analytics", ReferenceKey: "analytics-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateSecretRecord(ctx, domain.SecretRecord{
		ID: "second-secret", WorkspaceID: w1.ID, Name: "search", ReferenceKey: "search-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateSecretRecord(ctx, domain.SecretRecord{
		ID: "foreign-secret", WorkspaceID: w2.ID, Name: "foreign", ReferenceKey: "foreign-token",
	})
	if err != nil {
		t.Fatal(err)
	}

	script, err := s.CreateScript(ctx, domain.Script{
		ID: "bound-script", WorkspaceID: w1.ID, Name: "analytics-summary", Path: "/scripts/analytics-summary", Enabled: true,
		CredentialBindings: []domain.ScriptCredentialBinding{
			{Env: "POSTHOG_API_KEY", SecretRecordID: first.ID, CredentialIntegration: "caller-cannot-redirect", CredentialName: "caller-cannot-redirect"},
			{Env: "SEARCH_TOKEN", SecretRecordID: second.ID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if script.Access != domain.ScriptAccessRead {
		t.Fatalf("default access = %q, want read", script.Access)
	}
	if len(script.CredentialBindings) != 2 || script.CredentialBindings[0].CredentialIntegration != domain.SecretCredentialIntegration ||
		script.CredentialBindings[0].CredentialName != first.ReferenceKey {
		t.Fatalf("created bindings were not safely hydrated: %#v", script.CredentialBindings)
	}

	loaded, err := s.GetScript(ctx, script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.CredentialBindings) != 2 || loaded.CredentialBindings[0].Env != "POSTHOG_API_KEY" ||
		loaded.CredentialBindings[1].CredentialName != second.ReferenceKey {
		t.Fatalf("GetScript bindings = %#v", loaded.CredentialBindings)
	}
	listed, err := s.ListScripts(ctx, ScriptFilter{WorkspaceID: w1.ID})
	if err != nil || len(listed) != 1 || len(listed[0].CredentialBindings) != 2 {
		t.Fatalf("ListScripts bindings = %#v, %v", listed, err)
	}

	// An omitted binding slice preserves existing bindings during ordinary
	// metadata updates, while access remains a validated persisted property.
	loaded.Name = "renamed-script"
	loaded.Access = domain.ScriptAccessWrite
	loaded.CredentialBindings = nil
	if err := s.UpdateScript(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.GetScript(ctx, script.ID)
	if err != nil || loaded.Access != domain.ScriptAccessWrite || len(loaded.CredentialBindings) != 2 {
		t.Fatalf("updated script = %#v, %v", loaded, err)
	}

	original, err := s.SetScriptCredentialBindingsForWorkspace(ctx, w1.ID, script.ID, []domain.ScriptCredentialBinding{
		{Env: "POSTHOG_API_KEY", SecretRecordID: first.ID},
	})
	if err != nil || len(original) != 1 {
		t.Fatalf("set bindings = %#v, %v", original, err)
	}
	for _, invalid := range []domain.ScriptCredentialBinding{
		{Env: "lowercase", SecretRecordID: first.ID},
		{Env: "PATH", SecretRecordID: first.ID},
		{Env: "LD_PRELOAD", SecretRecordID: first.ID},
		{Env: "API_TOKEN", SecretRecordID: foreign.ID},
	} {
		if _, err := s.SetScriptCredentialBindingsForWorkspace(ctx, w1.ID, script.ID, []domain.ScriptCredentialBinding{invalid}); err == nil {
			t.Fatalf("unsafe or foreign binding was accepted: %#v", invalid)
		}
		got, getErr := s.ListScriptCredentialBindingsForWorkspace(ctx, w1.ID, script.ID)
		if getErr != nil || len(got) != 1 || got[0].SecretRecordID != first.ID {
			t.Fatalf("invalid replacement changed bindings: %#v, %v", got, getErr)
		}
	}
	if _, err := s.SetScriptCredentialBindingsForWorkspace(ctx, w1.ID, script.ID, []domain.ScriptCredentialBinding{
		{Env: "DUPLICATE_TOKEN", SecretRecordID: first.ID},
		{Env: "DUPLICATE_TOKEN", SecretRecordID: second.ID},
	}); err == nil {
		t.Fatal("duplicate environment variable binding was accepted")
	}
	gotAfterDuplicate, err := s.ListScriptCredentialBindingsForWorkspace(ctx, w1.ID, script.ID)
	if err != nil || len(gotAfterDuplicate) != 1 || gotAfterDuplicate[0].SecretRecordID != first.ID {
		t.Fatalf("duplicate replacement changed bindings: %#v, %v", gotAfterDuplicate, err)
	}

	// Script metadata and its binding replacement share one transaction.
	loaded, err = s.GetScript(ctx, script.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Name = "must-roll-back"
	loaded.CredentialBindings = []domain.ScriptCredentialBinding{{Env: "FOREIGN_TOKEN", SecretRecordID: foreign.ID}}
	if err := s.UpdateScript(ctx, loaded); err == nil {
		t.Fatal("cross-workspace binding update succeeded")
	}
	loaded, err = s.GetScript(ctx, script.ID)
	if err != nil || loaded.Name != "renamed-script" {
		t.Fatalf("failed script update was not atomic: %#v, %v", loaded, err)
	}

	if err := s.DeleteSecretRecordForWorkspace(ctx, w1.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.ListScriptCredentialBindingsForWorkspace(ctx, w1.ID, script.ID)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("deleted record left stale bindings: %#v, %v", bindings, err)
	}
	if _, err := s.CreateScript(ctx, domain.Script{
		ID: "invalid-access", WorkspaceID: w1.ID, Name: "invalid", Path: "/scripts/invalid", Access: "execute",
	}); err == nil {
		t.Fatal("invalid script access was accepted")
	}
	if _, err := s.CreateScript(ctx, domain.Script{
		ID: "cross-scope-script", WorkspaceID: w1.ID, Name: "cross-scope", Path: "/scripts/cross-scope",
		CredentialBindings: []domain.ScriptCredentialBinding{{Env: "API_TOKEN", SecretRecordID: foreign.ID}},
	}); err == nil {
		t.Fatal("cross-workspace binding create succeeded")
	}
	if _, err := s.GetScript(ctx, "cross-scope-script"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid create was not rolled back: %v", err)
	}
}

func TestScriptAccessColumnDefaultsLegacyInsertsToRead(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, domain.Workspace{ID: "legacy-script-w", Name: "Legacy", Path: "/legacy-script"})
	if err != nil {
		t.Fatal(err)
	}
	now := formatTime(s.now())
	if _, err := s.db.ExecContext(ctx, `INSERT INTO scripts
(id, workspace_id, name, path, description, enabled, timeout_seconds, created_at, updated_at)
VALUES ('legacy-script', ?, 'legacy', '/scripts/legacy', '', 1, 0, ?, ?)`, workspace.ID, now, now); err != nil {
		t.Fatal(err)
	}
	script, err := s.GetScript(ctx, "legacy-script")
	if err != nil {
		t.Fatal(err)
	}
	if script.Access != domain.ScriptAccessRead {
		t.Fatalf("legacy insert access = %q, want read", script.Access)
	}
}
