package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
)

const localAppColumns = `id, workspace_id, name, description, directory, command_json,
port, health_path, auto_start, created_at, updated_at`

type LocalAppFilter struct {
	WorkspaceID string
	AutoStart   *bool
	Limit       int
}

func (s *Store) CreateLocalApp(ctx context.Context, app domain.LocalApp) (domain.LocalApp, error) {
	var err error
	app.WorkspaceID, err = s.defaultWorkspaceID(ctx, app.WorkspaceID)
	if err != nil {
		return domain.LocalApp{}, err
	}
	if err := validateLocalApp(app); err != nil {
		return domain.LocalApp{}, err
	}
	if app.ID == "" {
		app.ID, err = newID()
		if err != nil {
			return domain.LocalApp{}, err
		}
	}
	now := s.now()
	app.CreatedAt = defaultTime(app.CreatedAt, now)
	app.UpdatedAt = defaultTime(app.UpdatedAt, app.CreatedAt)
	command, err := json.Marshal(app.Command)
	if err != nil {
		return domain.LocalApp{}, fmt.Errorf("store: encode local app command: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO local_apps (`+localAppColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		app.ID, app.WorkspaceID, app.Name, app.Description, app.Directory, command, app.Port,
		app.HealthPath, app.AutoStart, formatTime(app.CreatedAt), formatTime(app.UpdatedAt)); err != nil {
		return domain.LocalApp{}, fmt.Errorf("store: create local app: %w", err)
	}
	return app, nil
}

func (s *Store) GetLocalApp(ctx context.Context, id string) (domain.LocalApp, error) {
	return scanLocalApp(s.db.QueryRowContext(ctx, "SELECT "+localAppColumns+" FROM local_apps WHERE id = ?", id))
}

func (s *Store) GetLocalAppForWorkspace(ctx context.Context, workspaceID, id string) (domain.LocalApp, error) {
	return scanLocalApp(s.db.QueryRowContext(ctx, "SELECT "+localAppColumns+" FROM local_apps WHERE workspace_id = ? AND id = ?", workspaceID, id))
}

func (s *Store) ListLocalApps(ctx context.Context, filter LocalAppFilter) ([]domain.LocalApp, error) {
	workspaceID, err := s.defaultWorkspaceID(ctx, filter.WorkspaceID)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + localAppColumns + " FROM local_apps WHERE workspace_id = ?"
	args := []any{workspaceID}
	if filter.AutoStart != nil {
		query += " AND auto_start = ?"
		args = append(args, *filter.AutoStart)
	}
	query += " ORDER BY name, id"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list local apps: %w", err)
	}
	defer rows.Close()
	apps := []domain.LocalApp{}
	for rows.Next() {
		app, scanErr := scanLocalApp(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list local apps: %w", err)
	}
	return apps, nil
}

func (s *Store) UpdateLocalApp(ctx context.Context, app domain.LocalApp) error {
	if err := validateLocalApp(app); err != nil {
		return err
	}
	command, err := json.Marshal(app.Command)
	if err != nil {
		return fmt.Errorf("store: encode local app command: %w", err)
	}
	app.UpdatedAt = defaultTime(app.UpdatedAt, s.now())
	result, err := s.db.ExecContext(ctx, `UPDATE local_apps SET name = ?, description = ?, directory = ?,
command_json = ?, port = ?, health_path = ?, auto_start = ?, updated_at = ? WHERE workspace_id = ? AND id = ?`,
		app.Name, app.Description, app.Directory, command, app.Port, app.HealthPath, app.AutoStart,
		formatTime(app.UpdatedAt), app.WorkspaceID, app.ID)
	if err != nil {
		return fmt.Errorf("store: update local app: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update local app result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("local app %q: %w", app.ID, ErrNotFound)
	}
	return nil
}

func (s *Store) DeleteLocalAppForWorkspace(ctx context.Context, workspaceID, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM local_apps WHERE workspace_id = ? AND id = ?", workspaceID, id)
	if err != nil {
		return fmt.Errorf("store: delete local app: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete local app result: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("local app %q: %w", id, ErrNotFound)
	}
	return nil
}

func validateLocalApp(app domain.LocalApp) error {
	if strings.TrimSpace(app.WorkspaceID) == "" || strings.TrimSpace(app.Name) == "" || strings.TrimSpace(app.Directory) == "" {
		return fmt.Errorf("store: local app workspace, name, and directory are required")
	}
	if len(app.Command) == 0 || strings.TrimSpace(app.Command[0]) == "" {
		return fmt.Errorf("store: local app command is required")
	}
	if len(app.Command) > 32 {
		return fmt.Errorf("store: local app command exceeds 32 arguments")
	}
	switch strings.ToLower(filepath.Base(strings.TrimSpace(app.Command[0]))) {
	case "sh", "bash", "zsh", "fish", "dash", "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return fmt.Errorf("store: local app shell wrappers are not supported")
	}
	for _, argument := range app.Command {
		if strings.ContainsRune(argument, '\x00') || len(argument) > 4096 {
			return fmt.Errorf("store: local app command contains an invalid argument")
		}
	}
	if app.Port < 1024 || app.Port > 65535 {
		return fmt.Errorf("store: local app port must be between 1024 and 65535")
	}
	return nil
}

func scanLocalApp(row rowScanner) (domain.LocalApp, error) {
	var app domain.LocalApp
	var command []byte
	var created, updated string
	if err := row.Scan(&app.ID, &app.WorkspaceID, &app.Name, &app.Description, &app.Directory, &command,
		&app.Port, &app.HealthPath, &app.AutoStart, &created, &updated); err != nil {
		return domain.LocalApp{}, fmt.Errorf("store: get local app: %w", notFound("local app", err))
	}
	if err := json.Unmarshal(command, &app.Command); err != nil {
		return domain.LocalApp{}, fmt.Errorf("store: decode local app command: %w", err)
	}
	var err error
	app.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.LocalApp{}, err
	}
	app.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.LocalApp{}, err
	}
	return app, nil
}

var _ rowScanner = (*sql.Row)(nil)
