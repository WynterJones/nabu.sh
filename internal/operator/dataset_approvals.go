package operator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nabu-sh/nabu/internal/domain"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/steering"
)

const datasetDeleteApprovalKind = "dataset_delete_rows"

type datasetDeleteApproval struct {
	Kind        string                  `json:"kind"`
	WorkspaceID string                  `json:"workspace_id"`
	Targets     []datasetDeletionTarget `json:"targets"`
}

type datasetDeletionTarget struct {
	DatasetID string `json:"dataset_id"`
	RowID     int64  `json:"row_id"`
}

func (o *Operator) requestDatasetDeletionApproval(ctx context.Context, workspaceID, taskID, runID string, targets []datasetDeletionTarget) (domain.Approval, error) {
	enforcement := steering.EvaluateAction(domain.Policy{Dangerous: "ask"}, steering.ActionDeleteProductionData)
	if enforcement.Decision != steering.DecisionAsk || len(targets) == 0 || len(targets) > runner.MaxRunDatasetWrites {
		return domain.Approval{}, fmt.Errorf("invalid dataset deletion approval request")
	}
	for _, target := range targets {
		if target.DatasetID == "" || target.RowID <= 0 {
			return domain.Approval{}, fmt.Errorf("dataset deletion requires exact dataset and row IDs")
		}
		if _, err := o.store.GetDatasetRowForWorkspace(ctx, workspaceID, target.DatasetID, target.RowID); err != nil {
			return domain.Approval{}, translateDatasetError(err)
		}
	}
	metadata, _ := json.Marshal(datasetDeleteApproval{Kind: datasetDeleteApprovalKind, WorkspaceID: workspaceID, Targets: targets})
	approval, err := o.store.CreateApproval(ctx, domain.Approval{
		WorkspaceID: workspaceID, TaskID: taskID, RunID: runID, Status: domain.ApprovalPending,
		ProposedAction: fmt.Sprintf("Delete %d exact dataset row(s)", len(targets)),
		Why:            "Deleting durable workspace data is always approval-bound.",
		ProposedChange: "Permanently delete only the listed dataset row IDs.", EvidenceMetadata: metadata,
	})
	if err == nil {
		o.emitForWorkspace(ctx, workspaceID, "approval.created", approval.ID, approval)
	}
	return approval, err
}

func parseDatasetDeleteApproval(approval domain.Approval) (datasetDeleteApproval, bool) {
	var action datasetDeleteApproval
	if json.Unmarshal(approval.EvidenceMetadata, &action) != nil || action.Kind != datasetDeleteApprovalKind || action.WorkspaceID != approval.WorkspaceID || len(action.Targets) == 0 || len(action.Targets) > runner.MaxRunDatasetWrites {
		return datasetDeleteApproval{}, false
	}
	return action, true
}

func (o *Operator) applyApprovedDatasetDeletion(ctx context.Context, approval domain.Approval) error {
	action, ok := parseDatasetDeleteApproval(approval)
	if !ok {
		return nil
	}
	for _, target := range action.Targets {
		if _, err := o.store.GetDatasetRowForWorkspace(ctx, action.WorkspaceID, target.DatasetID, target.RowID); err != nil {
			return translateDatasetError(err)
		}
	}
	for _, target := range action.Targets {
		if err := o.store.DeleteDatasetRowForWorkspace(ctx, action.WorkspaceID, target.DatasetID, target.RowID); err != nil {
			return translateDatasetError(err)
		}
		o.emitForWorkspace(ctx, action.WorkspaceID, "dataset.row.deleted", target.DatasetID, map[string]any{
			"row_id": target.RowID, "approval_id": approval.ID, "task_id": approval.TaskID, "run_id": approval.RunID,
		})
	}
	return nil
}
