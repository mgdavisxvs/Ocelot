package tracker

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// BackupScheduler runs periodic VACUUM INTO backups of the active SQLite shard
// without blocking the main WAL writers. SQLite's VACUUM INTO copies the DB
// to a new file with all free pages reclaimed — safe to run concurrently.
type BackupScheduler struct {
	db        *SQLiteShardManager
	backupDir string
	interval  time.Duration
	stop      chan struct{}
}

// NewBackupScheduler creates a scheduler that writes backups to backupDir
// every interval. Call Start() in a goroutine to begin.
func NewBackupScheduler(db *SQLiteShardManager, backupDir string, interval time.Duration) *BackupScheduler {
	return &BackupScheduler{
		db:        db,
		backupDir: backupDir,
		interval:  interval,
		stop:      make(chan struct{}),
	}
}

// Start runs the backup loop. It blocks until Stop() is called.
func (bs *BackupScheduler) Start() {
	if err := os.MkdirAll(bs.backupDir, 0755); err != nil {
		log.Printf("backup: cannot create backup dir %s: %v", bs.backupDir, err)
		return
	}

	ticker := time.NewTicker(bs.interval)
	defer ticker.Stop()

	log.Printf("backup: scheduler started (interval=%s, dir=%s)", bs.interval, bs.backupDir)

	for {
		select {
		case <-ticker.C:
			if err := bs.runBackup(); err != nil {
				log.Printf("backup: VACUUM INTO failed: %v", err)
			}
		case <-bs.stop:
			log.Println("backup: scheduler stopped")
			return
		}
	}
}

// Stop signals the backup loop to exit.
func (bs *BackupScheduler) Stop() {
	close(bs.stop)
}

func (bs *BackupScheduler) runBackup() error {
	bs.db.mu.RLock()
	srcPath := bs.db.currentPath
	activeDB := bs.db.currentDB
	bs.db.mu.RUnlock()

	if activeDB == nil || srcPath == "" {
		return nil
	}

	ts := time.Now().Format("20060102-150405")
	base := filepath.Base(srcPath)
	ext := filepath.Ext(base)
	name := base[:len(base)-len(ext)]
	destPath := filepath.Join(bs.backupDir, fmt.Sprintf("%s-backup-%s.db", name, ts))

	start := time.Now()
	if err := vacuumInto(activeDB, destPath); err != nil {
		return fmt.Errorf("VACUUM INTO %s: %w", destPath, err)
	}

	info, _ := os.Stat(destPath)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}

	log.Printf("backup: wrote %s (%.1f MB) in %s", destPath, float64(size)/1e6, time.Since(start).Round(time.Millisecond))

	// Rotate: keep only the 3 most recent backups for this shard.
	bs.pruneOldBackups(name, 3)
	return nil
}

func vacuumInto(db *sql.DB, destPath string) error {
	_, err := db.Exec(fmt.Sprintf("VACUUM INTO %q", destPath))
	return err
}

// pruneOldBackups removes all but the newest `keep` backups matching baseName.
func (bs *BackupScheduler) pruneOldBackups(baseName string, keep int) {
	pattern := filepath.Join(bs.backupDir, baseName+"-backup-*.db")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) <= keep {
		return
	}
	// Glob returns alphabetical order; backups use timestamps so newest = last.
	toDelete := matches[:len(matches)-keep]
	for _, f := range toDelete {
		if err := os.Remove(f); err != nil {
			log.Printf("backup: failed to remove old backup %s: %v", f, err)
		} else {
			log.Printf("backup: pruned old backup %s", f)
		}
	}
}
