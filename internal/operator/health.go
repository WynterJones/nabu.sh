package operator

import (
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nabu-sh/nabu/internal/domain"
)

func setStatusHealth(snapshot *domain.StatusSnapshot, codexState, reason string, retryAt *time.Time, root string) {
	snapshot.CodexState = codexState
	snapshot.CodexReason = reason
	snapshot.CodexRetryAt = retryAt
	snapshot.ServiceHealthy = true
	var stat syscall.Statfs_t
	if syscall.Statfs(root, &stat) == nil {
		snapshot.DiskFreeBytes = uint64(stat.Bavail) * uint64(stat.Bsize)
	}
	snapshot.LastBackupAt = latestBackup(filepath.Join(root, "backups"))
}

func latestBackup(directory string) *time.Time {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	var latest time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().After(latest) {
			latest = info.ModTime().UTC()
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}
