package steering

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestProposedPlanAndExplicitScheduleValidation(t *testing.T) {
	plan := Effect{Type: EffectCreatePlan, Plan: &PlanChange{
		Title: "Six-month launch plan", Objective: "Launch and establish a measurable growth loop",
		Items: []PlanItemChange{{Kind: domain.PlanItemMilestone, Title: "Foundation complete"}, {Kind: domain.PlanItemTask, Title: "Launch beta"}},
	}}
	validated, err := ValidateResult(resultWith(plan), testState())
	if err != nil || validated.Effects[0].Plan.Title != "Six-month launch plan" {
		t.Fatalf("plan validation = %#v, %v", validated, err)
	}

	schedule := Effect{Type: EffectCreateSchedule, Schedule: &ScheduleChange{
		Name: "Weekly review", Kind: domain.ScheduleOrient, Expression: "@weekly", Reason: "Review progress",
	}}
	if _, err := ValidateResult(resultWith(schedule), testState()); !errors.Is(err, ErrHighImpactEffect) {
		t.Fatalf("implicit schedule error = %v", err)
	}
	state := testState()
	state.ScheduleRequested = true
	if _, err := ValidateResult(resultWith(schedule), state); err != nil {
		t.Fatalf("explicit schedule rejected: %v", err)
	}
}

func TestTaskAndMilestonePlanningStayInsideRollingTwoWeekHorizon(t *testing.T) {
	inside := time.Now().UTC().Add(13 * 24 * time.Hour)
	outOfRange := time.Now().UTC().Add(15 * 24 * time.Hour)
	task := Effect{Type: EffectCreateTask, Task: &TaskChange{
		Title: "Review activation", Purpose: "Check measurable progress", Why: "Keeps the mission on track", Priority: domain.PriorityNormal,
		DefinitionOfDone: []string{"Activation is reviewed"}, PlannedAt: &inside,
	}}
	if _, err := ValidateResult(resultWith(task), testState()); err != nil {
		t.Fatalf("in-horizon task rejected: %v", err)
	}
	task.Task.PlannedAt = &outOfRange
	if _, err := ValidateResult(resultWith(task), testState()); err == nil || !strings.Contains(err.Error(), "14-day") {
		t.Fatalf("out-of-horizon task error = %v", err)
	}
	plan := Effect{Type: EffectCreatePlan, Plan: &PlanChange{
		Title: "Growth horizon", Objective: "Reach 100 users", Items: []PlanItemChange{{Kind: domain.PlanItemMilestone, Title: "Review first-week acquisition", PlannedAt: &outOfRange}},
	}}
	if _, err := ValidateResult(resultWith(plan), testState()); err == nil || !strings.Contains(err.Error(), "14-day") {
		t.Fatalf("out-of-horizon milestone error = %v", err)
	}
}

func TestClosedIntegrationEffectIsNotPartOfSteeringContract(t *testing.T) {
	_, err := ValidateResult(resultWith(Effect{Type: "create_integration"}), testState())
	if !errors.Is(err, ErrUnknownEffect) {
		t.Fatalf("legacy integration effect error = %v", err)
	}
}

func TestDatasetEffectsEnforceAuthoritativeIDsTypesAndConservativeCaps(t *testing.T) {
	state := testState()
	state.Datasets = []domain.Dataset{{
		ID: "contacts", Name: "Contacts", UniqueKey: []string{"email"},
		Schema: []domain.DatasetColumn{{Name: "email", Type: domain.DatasetString}, {Name: "score", Type: domain.DatasetInteger}},
	}}
	valid := Effect{Type: EffectUpsertDatasetRows, DatasetRows: &DatasetRowsChange{DatasetID: "contacts", Rows: []map[string]any{{"email": "a@example.com", "score": float64(7)}}}}
	if _, err := ValidateResult(resultWith(valid), state); err != nil {
		t.Fatalf("valid dataset upsert rejected: %v", err)
	}
	unknown := valid
	unknown.DatasetRows = &DatasetRowsChange{DatasetID: "invented", Rows: valid.DatasetRows.Rows}
	if _, err := ValidateResult(resultWith(unknown), state); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("invented dataset ID error = %v", err)
	}
	wrongType := valid
	wrongType.DatasetRows = &DatasetRowsChange{DatasetID: "contacts", Rows: []map[string]any{{"email": "a@example.com", "score": "high"}}}
	if _, err := ValidateResult(resultWith(wrongType), state); err == nil || !strings.Contains(err.Error(), "invalid value") {
		t.Fatalf("dataset type error = %v", err)
	}
	tooMany := make([]map[string]any, maxChatDatasetRows+1)
	for index := range tooMany {
		tooMany[index] = map[string]any{"email": fmt.Sprintf("%d@example.com", index), "score": index}
	}
	oversized := valid
	oversized.DatasetRows = &DatasetRowsChange{DatasetID: "contacts", Rows: tooMany}
	if _, err := ValidateResult(resultWith(oversized), state); err == nil || !strings.Contains(err.Error(), "1-100") {
		t.Fatalf("dataset cap error = %v", err)
	}
}

func TestPacketIncludesInventoryProductContextAndCredentialGuidance(t *testing.T) {
	packet, err := BuildPacket(PacketRequest{
		Mission: domain.Mission{Statement: "Build the product"}, UserMessage: "What should we connect?",
		Inventory: WorkspaceInventory{Empty: true, Entries: []InventoryEntry{}},
		Plans:     []domain.Plan{{ID: "plan-1", Title: "Launch", Status: domain.PlanProposed}},
		Schedules: []domain.Schedule{{ID: "schedule-1", Name: "Weekly", Kind: domain.ScheduleOrient, Enabled: true, Expression: "@weekly"}},
		Secrets:   []SecretSummary{{ID: "secret-1", Name: "analytics_api_key", Configured: true}},
		Scripts:   []ScriptSummary{{ID: "script-1", Name: "Analytics summary", Access: "read", SecretNames: []string{"analytics_api_key"}}},
		Datasets:  []domain.Dataset{{ID: "dataset-1", Name: "Leads", Schema: []domain.DatasetColumn{{Name: "email", Type: domain.DatasetString}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"workspace_inventory", "schedule-1", "plan-1", "secret-1", "script-1", "dataset-1", "protected setup card directly in Chat", "Never ask for or include credential values", "Never create a proprietary integration manifest"} {
		if !strings.Contains(packet, required) {
			t.Fatalf("packet missing %q", required)
		}
	}
}

func TestExactDatasetRowEffectsRequireBoundedQueryContext(t *testing.T) {
	state := testState()
	state.Datasets = []domain.Dataset{{ID: "contacts", Name: "Contacts", Schema: []domain.DatasetColumn{{Name: "email", Type: domain.DatasetString}}}}
	update := Effect{Type: EffectUpdateDatasetRow, DatasetRow: &DatasetRowChange{DatasetID: "contacts", RowID: 7, Values: map[string]any{"email": "new@example.com"}}}
	if _, err := ValidateResult(resultWith(update), state); err == nil || !strings.Contains(err.Error(), "dataset_query_results") {
		t.Fatalf("unseen row update error = %v", err)
	}
	state.DatasetQueries = []DatasetQueryContext{{DatasetID: "contacts", Rows: []domain.DatasetRow{{ID: 7, Values: map[string]any{"email": "old@example.com"}}}}}
	if _, err := ValidateResult(resultWith(update), state); err != nil {
		t.Fatalf("exact visible row update rejected: %v", err)
	}
	deleteEffect := Effect{Type: EffectDeleteDatasetRow, DatasetRow: &DatasetRowChange{DatasetID: "contacts", RowID: 7}}
	if _, err := ValidateResult(resultWith(deleteEffect), state); err != nil {
		t.Fatalf("exact visible row deletion request rejected: %v", err)
	}
}

func TestContextGateRejectsExecutionUntilExplicitConfirmation(t *testing.T) {
	state := testState()
	state.ContextGateEnabled = true
	create := Effect{Type: EffectCreateTask, Task: &TaskChange{
		Title: "Premature task", Purpose: "Act before setup", Why: "No", Priority: domain.PriorityNormal,
		DefinitionOfDone: []string{"Done"},
	}}
	if _, err := ValidateResult(resultWith(create), state); err == nil || !strings.Contains(err.Error(), "context is confirmed") {
		t.Fatalf("premature task error = %v", err)
	}
	contextUpdate := Effect{Type: EffectUpdateContext, Context: &ContextChange{Value: "The owner already has a website."}}
	if _, err := ValidateResult(resultWith(contextUpdate), state); err != nil {
		t.Fatalf("context update rejected during setup: %v", err)
	}
	secret := Effect{Type: EffectCreateSecret, Secret: &SecretChange{Name: "analytics_api_key", Label: "Analytics API key"}}
	if _, err := ValidateResult(resultWith(secret), state); err != nil {
		t.Fatalf("secret setup rejected during context gathering: %v", err)
	}
	complete := Effect{Type: EffectCompleteContext}
	if _, err := ValidateResult(resultWith(complete), state); err == nil || !strings.Contains(err.Error(), "explicit owner confirmation") {
		t.Fatalf("unconfirmed completion error = %v", err)
	}
	state.ContextConfirmed = true
	if _, err := ValidateResult(resultWith(complete), state); err != nil {
		t.Fatalf("confirmed completion rejected: %v", err)
	}
	if _, err := ValidateResult(resultWith(complete, create), state); err != nil {
		t.Fatalf("safe work after context completion in the same response was rejected: %v", err)
	}
	reordered, err := ValidateResult(resultWith(create, complete), state)
	if err != nil || reordered.Effects[0].Type != EffectCompleteContext {
		t.Fatalf("confirmed context transition was not normalized before safe work: %#v, %v", reordered, err)
	}
}

func TestSoulReflectionIsBoundedAndAllowedDuringContextSetup(t *testing.T) {
	state := testState()
	state.ContextGateEnabled = true
	effect := Effect{Type: EffectUpdateSoul, Soul: &SoulChange{Reflection: "The owner prefers one direct question at a time."}}
	result, err := ValidateResult(resultWith(effect), state)
	if err != nil || result.Effects[0].Soul.Reflection == "" {
		t.Fatalf("valid soul reflection rejected: %#v, %v", result, err)
	}
	effect.Soul.Reflection = strings.Repeat("x", 1_001)
	if _, err := ValidateResult(resultWith(effect), state); err == nil {
		t.Fatal("oversized soul reflection accepted")
	}
}

func TestChoiceRequestIsBoundedAndAllowedDuringSetup(t *testing.T) {
	state := testState()
	state.ContextGateEnabled = true
	choice := Effect{Type: EffectRequestChoice, Choice: &ChoiceRequest{
		Prompt: "Which repository should Nabu inspect first?",
		Options: []ChoiceOption{
			{Label: "Marketing site", Value: "Inspect the marketing-site repository first.", Primary: true},
			{Label: "Application", Value: "Inspect the application repository first."},
		},
	}}
	result, err := ValidateResult(resultWith(choice), state)
	if err != nil || len(result.Effects[0].Choice.Options) != 2 {
		t.Fatalf("valid choice rejected: %#v, %v", result, err)
	}
	choice.Choice.Options = choice.Choice.Options[:1]
	if _, err := ValidateResult(resultWith(choice), state); err == nil || !strings.Contains(err.Error(), "2-5") {
		t.Fatalf("undersized choice error = %v", err)
	}
}

func TestRecoveryTurnCanOnlyMutateItsTargetTask(t *testing.T) {
	state := testState()
	state.Tasks = append(state.Tasks, domain.Task{ID: "failed-2", Title: "Other failure", Status: domain.TaskFailed, Priority: domain.PriorityNormal})
	state.RecoveryTaskID = "task-1"
	valid := Effect{Type: EffectUpdateTask, TaskID: "task-1", Task: &TaskChange{Status: domain.TaskReady}}
	if _, err := ValidateResult(resultWith(valid), state); err != nil {
		t.Fatalf("targeted recovery update rejected: %v", err)
	}
	other := Effect{Type: EffectUpdateTask, TaskID: "failed-2", Task: &TaskChange{Status: domain.TaskReady}}
	if _, err := ValidateResult(resultWith(other), state); !errors.Is(err, ErrHighImpactEffect) {
		t.Fatalf("cross-task recovery error = %v", err)
	}
}
