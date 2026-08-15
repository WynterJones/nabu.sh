package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nabu-sh/nabu/internal/domain"
)

const messageColumns = `id, workspace_id, parent_message_id, thread_root_id, role, content,
status, effect, effect_metadata, created_at, updated_at`

type MessageFilter struct {
	WorkspaceID  string
	AfterID      int64
	BeforeID     int64
	Role         domain.MessageRole
	Limit        int
	TopLevelOnly bool
	ThreadRootID int64
}

func (s *Store) AppendMessage(ctx context.Context, message domain.Message) (domain.Message, error) {
	var err error
	message.WorkspaceID, err = s.defaultWorkspaceID(ctx, message.WorkspaceID)
	if err != nil {
		return domain.Message{}, err
	}
	if err := validateMessage(&message); err != nil {
		return domain.Message{}, err
	}
	now := s.now()
	message.CreatedAt = defaultTime(message.CreatedAt, now)
	message.UpdatedAt = defaultTime(message.UpdatedAt, message.CreatedAt)
	if message.ParentMessageID != nil {
		var rootID int64
		var parentWorkspace sql.NullString
		var parentRoot sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `
SELECT workspace_id, thread_root_id FROM messages WHERE id = ?`, *message.ParentMessageID).Scan(&parentWorkspace, &parentRoot); err != nil {
			return domain.Message{}, fmt.Errorf("store: find parent message: %w", notFound("message", err))
		}
		if parentWorkspace.String != message.WorkspaceID {
			return domain.Message{}, fmt.Errorf("store: parent message belongs to another workspace")
		}
		rootID = *message.ParentMessageID
		if parentRoot.Valid {
			rootID = parentRoot.Int64
		}
		message.ThreadRootID = &rootID
	} else {
		message.ThreadRootID = nil
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO messages(workspace_id, parent_message_id, thread_root_id, role, content, status, effect, effect_metadata, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, nullableText(message.WorkspaceID), nullableInt64(message.ParentMessageID),
		nullableInt64(message.ThreadRootID), message.Role, message.Content, message.Status, message.Effect,
		nullableBytes(message.EffectMetadata), formatTime(message.CreatedAt), formatTime(message.UpdatedAt))
	if err != nil {
		return domain.Message{}, fmt.Errorf("store: append message: %w", err)
	}
	message.ID, err = result.LastInsertId()
	if err != nil {
		return domain.Message{}, fmt.Errorf("store: message id: %w", err)
	}
	return message, nil
}

func (s *Store) GetMessage(ctx context.Context, id int64) (domain.Message, error) {
	message, err := scanMessage(s.db.QueryRowContext(ctx, "SELECT "+messageColumns+" FROM messages WHERE id = ?", id))
	if err != nil {
		return domain.Message{}, err
	}
	if message.ParentMessageID == nil {
		message.ReplyCount, err = s.CountThreadReplies(ctx, message.ID)
		if err != nil {
			return domain.Message{}, err
		}
	}
	return message, nil
}

// ListMessages returns messages in chronological order. A limited query first
// selects the newest matching page, then reverses it for display.
func (s *Store) ListMessages(ctx context.Context, filter MessageFilter) ([]domain.Message, error) {
	query := "SELECT " + messageColumns + " FROM messages WHERE 1 = 1"
	var args []any
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if filter.ThreadRootID > 0 {
		return s.listThreadMessages(ctx, workspaceID, filter.ThreadRootID, filter.BeforeID, filter.Limit)
	}
	query += " AND COALESCE(workspace_id, '') = ?"
	args = append(args, workspaceID)
	if filter.AfterID > 0 {
		query += " AND id > ?"
		args = append(args, filter.AfterID)
	}
	if filter.BeforeID > 0 {
		query += " AND id < ?"
		args = append(args, filter.BeforeID)
	}
	if filter.Role != "" {
		query += " AND role = ?"
		args = append(args, filter.Role)
	}
	if filter.TopLevelOnly {
		query += " AND parent_message_id IS NULL"
	}
	ascendingPage := filter.AfterID > 0 && filter.BeforeID == 0
	if ascendingPage {
		query += " ORDER BY id"
	} else {
		query += " ORDER BY id DESC"
	}
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	query += " LIMIT ?"
	args = append(args, filter.Limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list messages: %w", err)
	}
	defer rows.Close()
	var messages []domain.Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list messages: %w", err)
	}
	if !ascendingPage {
		for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
			messages[left], messages[right] = messages[right], messages[left]
		}
	}
	for i := range messages {
		if messages[i].ParentMessageID == nil {
			count, err := s.CountThreadReplies(ctx, messages[i].ID)
			if err != nil {
				return nil, err
			}
			messages[i].ReplyCount = count
		}
	}
	return messages, nil
}

func (s *Store) listThreadMessages(ctx context.Context, workspaceID string, rootID, beforeID int64, limit int) ([]domain.Message, error) {
	root, err := scanMessage(s.db.QueryRowContext(ctx, `
SELECT `+messageColumns+` FROM messages
WHERE id = ? AND parent_message_id IS NULL AND COALESCE(workspace_id, '') = ?`, rootID, workspaceID))
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	query := `
SELECT ` + messageColumns + ` FROM messages
WHERE thread_root_id = ? AND COALESCE(workspace_id, '') = ?
`
	args := []any{rootID, workspaceID}
	if beforeID > 0 {
		query += " AND id < ?"
		args = append(args, beforeID)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list thread replies: %w", err)
	}
	defer rows.Close()
	var replies []domain.Message
	for rows.Next() {
		reply, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		replies = append(replies, reply)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list thread replies: %w", err)
	}
	for left, right := 0, len(replies)-1; left < right; left, right = left+1, right-1 {
		replies[left], replies[right] = replies[right], replies[left]
	}
	root.ReplyCount, err = s.CountThreadReplies(ctx, rootID)
	if err != nil {
		return nil, err
	}
	return append([]domain.Message{root}, replies...), nil
}

func (s *Store) CountThreadReplies(ctx context.Context, rootID int64) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE thread_root_id = ?", rootID).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count thread replies: %w", err)
	}
	return count, nil
}

func (s *Store) UpdateMessage(ctx context.Context, message domain.Message) error {
	if err := validateMessage(&message); err != nil {
		return err
	}
	message.UpdatedAt = defaultTime(message.UpdatedAt, s.now())
	result, err := s.db.ExecContext(ctx, `
		UPDATE messages SET workspace_id = ?, role = ?, content = ?, status = ?, effect = ?, effect_metadata = ?, updated_at = ? WHERE id = ?`,
		nullableText(message.WorkspaceID), message.Role, message.Content, message.Status, message.Effect, nullableBytes(message.EffectMetadata),
		formatTime(message.UpdatedAt), message.ID)
	if err != nil {
		return fmt.Errorf("store: update message: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update message result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("message %d: %w", message.ID, ErrNotFound)
	}
	return nil
}

// ClaimNextQueuedMessage atomically reserves the oldest durable user message.
// The global FIFO is consumed by one Chat worker so conversation stays ordered
// even though Chat may run beside the autonomous task lane.
func (s *Store) ClaimNextQueuedMessage(ctx context.Context) (domain.Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Message{}, fmt.Errorf("store: begin chat claim: %w", err)
	}
	defer tx.Rollback()
	message, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+`
FROM messages WHERE role = 'user' AND status = 'queued' ORDER BY id LIMIT 1`))
	if err != nil {
		return domain.Message{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE messages SET status = 'processing', updated_at = ?
WHERE id = ? AND status = 'queued'`, formatTime(s.now()), message.ID)
	if err != nil {
		return domain.Message{}, fmt.Errorf("store: claim queued message: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return domain.Message{}, fmt.Errorf("store: queued message claim lost: %w", ErrClaimLost)
	}
	if err := tx.Commit(); err != nil {
		return domain.Message{}, fmt.Errorf("store: commit chat claim: %w", err)
	}
	message.Status = domain.MessageProcessing
	return message, nil
}

func (s *Store) QueuedMessageCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE role = 'user' AND status = 'queued'").Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count queued messages: %w", err)
	}
	return count, nil
}

func (s *Store) OpenMessageCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages
WHERE role = 'user' AND status IN ('queued','processing')`).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count open messages: %w", err)
	}
	return count, nil
}

func (s *Store) RequeueMessage(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE messages SET status = 'queued', updated_at = ?
WHERE id = ? AND role = 'user' AND status = 'processing'`, formatTime(s.now()), id)
	if err != nil {
		return fmt.Errorf("store: requeue message: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: requeue message result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("message %d: %w", id, ErrInvalidTransition)
	}
	return nil
}

func (s *Store) DeleteMessage(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM messages
WHERE id = ? AND status <> 'processing'
  AND NOT EXISTS (
      SELECT 1 FROM messages AS reply
      WHERE reply.thread_root_id = messages.id AND reply.status = 'processing'
  )`, id)
	if err != nil {
		return fmt.Errorf("store: delete message: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete message result: %w", err)
	}
	if count == 0 {
		var exists int
		if queryErr := s.db.QueryRowContext(ctx, "SELECT 1 FROM messages WHERE id = ?", id).Scan(&exists); queryErr == nil {
			return fmt.Errorf("message %d is processing: %w", id, ErrInvalidTransition)
		}
		return fmt.Errorf("message %d: %w", id, ErrNotFound)
	}
	return nil
}

func validateMessage(message *domain.Message) error {
	switch message.Role {
	case domain.MessageUser, domain.MessageAssistant, domain.MessageSystem:
	default:
		return fmt.Errorf("store: invalid message role %q", message.Role)
	}
	if message.Status == "" {
		message.Status = domain.MessageComplete
	}
	switch message.Status {
	case domain.MessageQueued, domain.MessageProcessing, domain.MessageComplete, domain.MessageFailed:
	default:
		return fmt.Errorf("store: invalid message status %q", message.Status)
	}
	if message.Role != domain.MessageUser && (message.Status == domain.MessageQueued || message.Status == domain.MessageProcessing) {
		return fmt.Errorf("store: only user messages may be queued or processing")
	}
	if message.Effect == "" {
		message.Effect = domain.EffectConversationOnly
	}
	switch message.Effect {
	case domain.EffectConversationOnly, domain.EffectProposeContext, domain.EffectRequestChoice, domain.EffectCompleteContext, domain.EffectCreateTask, domain.EffectUpdateTask,
		domain.EffectCancelTask, domain.EffectUpdateMission, domain.EffectUpdateContext,
		domain.EffectUpdatePolicy, domain.EffectApproveAction, domain.EffectRejectAction,
		domain.EffectPause, domain.EffectResume, domain.EffectRequestReport,
		domain.EffectCreatePlan, domain.EffectCreateSchedule, domain.EffectCreateIntegration,
		domain.EffectCreateSecret, domain.EffectCreateScript,
		domain.EffectCreateDataset, domain.EffectUpsertDatasetRows,
		domain.EffectUpdateDatasetRow, domain.EffectDeleteDatasetRow, domain.EffectUpdateSoul:
	default:
		return fmt.Errorf("store: invalid chat effect %q", message.Effect)
	}
	return nil
}

func scanMessage(row rowScanner) (domain.Message, error) {
	var message domain.Message
	var metadata []byte
	var created string
	var updated sql.NullString
	var workspaceID sql.NullString
	var parentID, threadRootID sql.NullInt64
	if err := row.Scan(&message.ID, &workspaceID, &parentID, &threadRootID, &message.Role, &message.Content, &message.Status, &message.Effect,
		&metadata, &created, &updated); err != nil {
		return domain.Message{}, fmt.Errorf("store: get message: %w", notFound("message", err))
	}
	message.WorkspaceID = workspaceID.String
	message.ParentMessageID = pointerInt64(parentID)
	message.ThreadRootID = pointerInt64(threadRootID)
	message.EffectMetadata = metadata
	var err error
	message.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Message{}, err
	}
	if updated.Valid {
		message.UpdatedAt, err = parseTime(updated.String)
	} else {
		message.UpdatedAt = message.CreatedAt
	}
	if err != nil {
		return domain.Message{}, err
	}
	return message, nil
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func pointerInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
