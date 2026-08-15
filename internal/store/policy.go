package store

import (
	"context"
	"fmt"

	"github.com/nabu-sh/nabu/internal/domain"
)

// GetPolicy returns the singleton structured autonomy policy.
func (s *Store) GetPolicy(ctx context.Context) (domain.Policy, error) {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return domain.Policy{}, err
	}
	if workspaceID != "" {
		return s.GetPolicyForWorkspace(ctx, workspaceID)
	}
	return s.getLegacyPolicy(ctx)
}

func (s *Store) getLegacyPolicy(ctx context.Context) (domain.Policy, error) {
	var policy domain.Policy
	if err := s.db.QueryRowContext(ctx, `SELECT read, work, publish, dangerous FROM policy WHERE id = 1`).Scan(
		&policy.Read, &policy.Work, &policy.Publish, &policy.Dangerous,
	); err != nil {
		return domain.Policy{}, fmt.Errorf("store: get policy: %w", notFound("policy", err))
	}
	return policy, nil
}

func (s *Store) GetPolicyForWorkspace(ctx context.Context, workspaceID string) (domain.Policy, error) {
	var policy domain.Policy
	if err := s.db.QueryRowContext(ctx, `
SELECT read, work, publish, dangerous FROM workspace_policies WHERE workspace_id = ?`, workspaceID).Scan(
		&policy.Read, &policy.Work, &policy.Publish, &policy.Dangerous,
	); err != nil {
		return domain.Policy{}, fmt.Errorf("store: get workspace policy: %w", notFound("policy", err))
	}
	return policy, nil
}

// UpdatePolicy replaces the singleton structured autonomy policy.
func (s *Store) UpdatePolicy(ctx context.Context, policy domain.Policy) error {
	workspaceID, err := s.activeWorkspaceID(ctx)
	if err != nil {
		return err
	}
	if workspaceID != "" {
		return s.UpdatePolicyForWorkspace(ctx, workspaceID, policy)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE policy SET read = ?, work = ?, publish = ?, dangerous = ? WHERE id = 1`,
		policy.Read, policy.Work, policy.Publish, policy.Dangerous)
	if err != nil {
		return fmt.Errorf("store: update policy: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update policy result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("policy: %w", ErrNotFound)
	}
	return nil
}

func (s *Store) UpdatePolicyForWorkspace(ctx context.Context, workspaceID string, policy domain.Policy) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE workspace_policies SET read = ?, work = ?, publish = ?, dangerous = ? WHERE workspace_id = ?`,
		policy.Read, policy.Work, policy.Publish, policy.Dangerous, workspaceID)
	if err != nil {
		return fmt.Errorf("store: update workspace policy: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update workspace policy result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("policy for workspace %q: %w", workspaceID, ErrNotFound)
	}
	return nil
}
