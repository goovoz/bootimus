package storage

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	restorePendingDir    = ".restore-pending"
	restoreSummaryFile   = ".restore-summary.json"
	restoreMaxArchive    = int64(2 << 30)
	sqliteDatabaseFile   = "bootimus.db"
	postgresDatabaseDump = "bootimus.sql"
)

type RestoreSummary struct {
	Database  string    `json:"database"`
	FileCount int       `json:"file_count"`
	Bytes     int64     `json:"bytes"`
	StagedAt  time.Time `json:"staged_at"`
}

var ErrNoPendingRestore = errors.New("no restore is staged")

func stagingPath(dataDir string) string {
	return filepath.Join(filepath.Clean(dataDir), restorePendingDir)
}

func StageRestore(dataDir string, archive io.Reader) (*RestoreSummary, error) {
	dataDir = filepath.Clean(dataDir)
	if dataDir == "" || dataDir == "." {
		return nil, errors.New("data directory not configured")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare data directory: %w", err)
	}
	tmpDir, err := os.MkdirTemp(dataDir, ".restore-upload-*")
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	summary, err := extractRestoreArchive(tmpDir, archive)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}
	summary.StagedAt = time.Now().UTC()
	if err := writeRestoreSummary(tmpDir, summary); err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}
	final := stagingPath(dataDir)
	os.RemoveAll(final)
	if err := os.Rename(tmpDir, final); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("stage restore: %w", err)
	}
	return summary, nil
}

func extractRestoreArchive(dest string, archive io.Reader) (*RestoreSummary, error) {
	limited := &io.LimitedReader{R: archive, N: restoreMaxArchive + 1}
	gz, err := gzip.NewReader(limited)
	if err != nil {
		return nil, errors.New("backup must be a gzip-compressed tar archive")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	summary := &RestoreSummary{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if limited.N <= 0 {
				return nil, fmt.Errorf("backup exceeds the %d GiB limit", restoreMaxArchive>>30)
			}
			return nil, fmt.Errorf("read backup archive: %w", err)
		}
		rel, err := safeArchivePath(hdr.Name)
		if err != nil {
			return nil, err
		}
		if rel == "" {
			continue
		}
		target := filepath.Join(dest, rel)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return nil, err
			}
			n, copyErr := io.Copy(f, tr)
			f.Close()
			if copyErr != nil {
				if limited.N <= 0 {
					return nil, fmt.Errorf("backup exceeds the %d GiB limit", restoreMaxArchive>>30)
				}
				return nil, fmt.Errorf("extract %s: %w", rel, copyErr)
			}
			summary.FileCount++
			summary.Bytes += n
			if !strings.Contains(rel, "/") {
				switch rel {
				case sqliteDatabaseFile:
					summary.Database = "sqlite"
				case postgresDatabaseDump:
					summary.Database = "postgres"
				}
			}
		default:
			return nil, fmt.Errorf("backup contains an unsupported entry %q (symbolic links and devices are not allowed)", hdr.Name)
		}
		if limited.N <= 0 {
			return nil, fmt.Errorf("backup exceeds the %d GiB limit", restoreMaxArchive>>30)
		}
	}
	if summary.Database == "" {
		return nil, errors.New("backup does not contain bootimus.db or bootimus.sql at the top level")
	}
	return summary, nil
}

func safeArchivePath(name string) (string, error) {
	cleaned := strings.TrimPrefix(filepath.ToSlash(name), "./")
	cleaned = strings.TrimSuffix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return "", nil
	}
	if strings.HasPrefix(cleaned, "/") || filepath.VolumeName(cleaned) != "" {
		return "", fmt.Errorf("backup contains an absolute path %q", name)
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." {
			return "", fmt.Errorf("backup contains an unsafe path %q", name)
		}
	}
	if cleaned == restorePendingDir || strings.HasPrefix(cleaned, restorePendingDir+"/") || cleaned == restoreSummaryFile {
		return "", fmt.Errorf("backup contains a reserved path %q", name)
	}
	return filepath.FromSlash(cleaned), nil
}

func writeRestoreSummary(dir string, summary *RestoreSummary) error {
	body, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, restoreSummaryFile), body, 0o600)
}

func PendingRestore(dataDir string) (*RestoreSummary, error) {
	body, err := os.ReadFile(filepath.Join(stagingPath(dataDir), restoreSummaryFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoPendingRestore
	}
	if err != nil {
		return nil, err
	}
	var summary RestoreSummary
	if err := json.Unmarshal(body, &summary); err != nil {
		return nil, fmt.Errorf("read staged restore summary: %w", err)
	}
	return &summary, nil
}

func CancelRestore(dataDir string) error {
	if _, err := PendingRestore(dataDir); err != nil {
		return err
	}
	return os.RemoveAll(stagingPath(dataDir))
}

func ApplyPendingRestore(dataDir string, pg *Config) (*RestoreSummary, error) {
	summary, err := PendingRestore(dataDir)
	if err != nil {
		return nil, err
	}
	dataDir = filepath.Clean(dataDir)
	staged := stagingPath(dataDir)

	switch summary.Database {
	case "sqlite":
		if pg != nil {
			return nil, errors.New("staged backup holds a SQLite database but Bootimus is configured for PostgreSQL")
		}
		if err := applySQLiteRestore(dataDir, staged); err != nil {
			return nil, err
		}
	case "postgres":
		if pg == nil {
			return nil, errors.New("staged backup holds a PostgreSQL dump but Bootimus is configured for SQLite")
		}
		if err := applyPostgresRestore(pg, filepath.Join(staged, postgresDatabaseDump)); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("staged backup has unknown database kind %q", summary.Database)
	}

	if err := copyStagedFiles(staged, dataDir); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(staged); err != nil {
		return nil, fmt.Errorf("remove staging directory: %w", err)
	}
	return summary, nil
}

func applySQLiteRestore(dataDir, staged string) error {
	current := filepath.Join(dataDir, sqliteDatabaseFile)
	incoming := filepath.Join(staged, sqliteDatabaseFile)
	if _, err := os.Stat(incoming); err != nil {
		return fmt.Errorf("staged database missing: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		os.Remove(current + suffix)
	}
	if _, err := os.Stat(current); err == nil {
		keep := current + ".pre-restore"
		os.Remove(keep)
		if err := os.Rename(current, keep); err != nil {
			return fmt.Errorf("keep previous database: %w", err)
		}
	}
	if err := os.Rename(incoming, current); err != nil {
		return fmt.Errorf("install restored database: %w", err)
	}
	return nil
}

func applyPostgresRestore(cfg *Config, dumpPath string) error {
	if _, err := exec.LookPath("psql"); err != nil {
		return fmt.Errorf("psql not found on PATH: %w (install postgresql-client in your image)", err)
	}
	dump, err := os.Open(dumpPath)
	if err != nil {
		return fmt.Errorf("open staged dump: %w", err)
	}
	defer dump.Close()

	cmd := exec.Command("psql",
		"--host="+cfg.Host,
		"--port="+strconv.Itoa(cfg.Port),
		"--username="+cfg.User,
		"--dbname="+cfg.DBName,
		"--quiet",
		"--set=ON_ERROR_STOP=1",
	)
	cmd.Env = append(cmd.Environ(),
		"PGPASSWORD="+cfg.Password,
		"PGSSLMODE="+cfg.SSLMode,
	)
	cmd.Stdin = dump
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql restore failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func copyStagedFiles(staged, dataDir string) error {
	return filepath.Walk(staged, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(staged, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == restoreSummaryFile || rel == sqliteDatabaseFile || rel == postgresDatabaseDump {
			return nil
		}
		target := filepath.Join(dataDir, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.Rename(path, target); err == nil {
			return nil
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close()
			return err
		}
		return dst.Close()
	})
}
