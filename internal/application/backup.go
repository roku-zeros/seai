package application

import (
	"context"
	"path/filepath"
	"seai.local/seai/internal/domain"
	"seai.local/seai/internal/maintenance"
)

func (s *Service) backup(ctx context.Context, run string, _ domain.Task) (any, error) {
	path := filepath.Join(s.Data, "backups", run)
	manifest, e := maintenance.Backup(ctx, s.Store.DB, s.Data, path)
	return map[string]any{"backup_path": path, "manifest": manifest, "restore": "Stop daemon; seai --data NEW_DIRECTORY backup restore --from BACKUP_PATH"}, e
}
