package operator

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/steering"
	"github.com/nabu-sh/nabu/internal/store"
)

const (
	idleMinimumDuration = 15 * time.Minute
	idleReviewLease     = 45 * time.Minute
	idleUsefulCooldown  = 6 * time.Hour
	idleNoWorkCooldown  = 12 * time.Hour
	idleFailureCooldown = 45 * time.Minute
)

type idleStewardshipState struct {
	ReviewedAt time.Time                   `json:"reviewed_at"`
	Inventory  steering.WorkspaceInventory `json:"workspace_inventory"`
	Schedules  []domain.Schedule           `json:"schedules"`
	Plans      []domain.Plan               `json:"plans"`
	Secrets    []steering.SecretSummary    `json:"saved_secret_metadata"`
	Scripts    []steering.ScriptSummary    `json:"registered_scripts"`
	Datasets   []domain.Dataset            `json:"datasets"`
}

func (o *Operator) idleStewardContext(ctx context.Context, workspace domain.Workspace, now time.Time) (json.RawMessage, error) {
	inventory, err := inspectWorkspaceInventory(workspace.Path)
	if err != nil {
		return nil, err
	}
	schedules, err := o.store.ListSchedules(ctx, store.ScheduleFilter{WorkspaceID: workspace.ID, Limit: 50})
	if err != nil {
		return nil, err
	}
	plans, err := o.store.ListPlans(ctx, store.PlanFilter{WorkspaceID: workspace.ID, Statuses: []domain.PlanStatus{domain.PlanProposed, domain.PlanActive}, Limit: 25})
	if err != nil {
		return nil, err
	}
	secretRecords, err := o.store.ListSecretRecords(ctx, store.SecretRecordFilter{WorkspaceID: workspace.ID, Limit: 64})
	if err != nil {
		return nil, err
	}
	secretSummaries := make([]steering.SecretSummary, 0, len(secretRecords))
	secretNames := make(map[string]string, len(secretRecords))
	for _, record := range secretRecords {
		configured, configuredErr := o.secretConfigured(ctx, record)
		if configuredErr != nil {
			return nil, configuredErr
		}
		secretSummaries = append(secretSummaries, steering.SecretSummary{ID: record.ID, Name: record.Name, Description: record.Description, Configured: configured})
		secretNames[record.ID] = record.Name
	}
	scriptRecords, err := o.store.ListScripts(ctx, store.ScriptFilter{WorkspaceID: workspace.ID, Limit: 64})
	if err != nil {
		return nil, err
	}
	scriptSummaries := make([]steering.ScriptSummary, 0, len(scriptRecords))
	for _, script := range scriptRecords {
		names := make([]string, 0, len(script.CredentialBindings))
		for _, binding := range script.CredentialBindings {
			if name := secretNames[binding.SecretRecordID]; name != "" {
				names = append(names, name)
			}
		}
		scriptSummaries = append(scriptSummaries, steering.ScriptSummary{ID: script.ID, Name: script.Name, Description: script.Description, Access: string(script.Access), SecretNames: names})
	}
	datasets, err := o.store.ListDatasets(ctx, store.DatasetFilter{WorkspaceID: workspace.ID, Limit: 50})
	if err != nil {
		return nil, err
	}
	return json.Marshal(idleStewardshipState{
		ReviewedAt: now.UTC(), Inventory: inventory, Schedules: schedules, Plans: plans,
		Secrets: secretSummaries, Scripts: scriptSummaries, Datasets: datasets,
	})
}

func idleStewardCooldown(noWork bool) time.Duration {
	if noWork {
		return idleNoWorkCooldown
	}
	return idleUsefulCooldown
}
