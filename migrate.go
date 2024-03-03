package svc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Interface that impls both fs.ReadFileFS and fs.ReadDirFS
//
// e.g.,
//
//	//go:embed schema/svc/*.sql
//	var schemaFs embed.FS
type ReadFS interface {
	fs.ReadFileFS
	fs.ReadDirFS
}

type MigrateConfig struct {
	App     string
	Fs      ReadFS
	BaseDir string

	// Starting version, it's optional. If provided, svc tries to start with the provided version.
	// If absent, svc follows the previous version.
	StartingVersion string
}

func MigrateSchema(db *gorm.DB, log Logger, c MigrateConfig) error {
	start := time.Now()
	defer func() { log.Infof("Migrate schema took %v", time.Since(start)) }()

	if c.Fs == nil {
		return errors.New("fs is nil")
	}
	if log == nil {
		return errors.New("log is nil")
	}
	if db == nil {
		return errors.New("db is nil")
	}

	t := db.Exec(`
	CREATE TABLE IF NOT EXISTS schema_version (
		id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
		app VARCHAR(50) NOT NULL DEFAULT '',
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		script VARCHAR(256) NOT NULL DEFAULT '',
		success TINYINT(1) NOT NULL DEFAULT 1,
		remark VARCHAR(256) NOT NULL DEFAULT '',
		PRIMARY KEY (id),
		KEY app_idx (app)
	)`)
	if t.Error != nil {
		return fmt.Errorf("failed to create schema_verion table, %w", t.Error)
	}

	var last string
	if c.StartingVersion != "" {
		last = c.StartingVersion
	}

	lastVer := new(schemaVersion)
	t = db.Raw(`
		SELECT id, script, success, remark
		FROM schema_version
		WHERE app = ?
		ORDER BY id DESC LIMIT 1`, c.App).Scan(lastVer)
	if t.Error != nil {
		return fmt.Errorf("failed to list schema_verion, %w", t.Error)
	}
	if t.RowsAffected < 1 {
		lastVer = nil
	} else if !lastVer.Success {
		return fmt.Errorf("previous schema migration was failed, last attempt was '%v' (%v), please fix the execution manually and update the last 'schema_version' record status (id: %v)", lastVer.Script, lastVer.Remark, lastVer.Id)
	}

	// e.g.,
	//
	// 	StartingVersion: v0.0.3, lastVer: v0.0.4, we pick v0.0.4
	// 	StartingVersion: v0.0.3, lastVer: v0.0.2, we pick v0.0.3
	// 	StartingVersion: v0.0.3, lastVer: nil,    we pick v0.0.3
	// 	StartingVersion: nil   , lastVer: v0.0.1, we pick v0.0.1
	if lastVer != nil {
		if last != "" {
			if VerAfter(lastVer.Script, last) {
				last = lastVer.Script
			}
		} else {
			last = lastVer.Script
		}
	}
	if last != "" {
		log.Infof("Migrate schema version starting from '%s'", last)
	}

	files, err := c.Fs.ReadDir(c.BaseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to open %v folders, %w", c.BaseDir, err)
	}

	schemaFiles := convertSchemaFiles(last, files, c.BaseDir, c.Fs)
	sortSchemaFile(schemaFiles)

	for _, sf := range schemaFiles {
		content, err := sf.Read()
		if err != nil {
			return err
		}
		if err := runSQLFile(db, log, c.App, content, sf.Name); err != nil {
			return fmt.Errorf("failed to exec sql file %v, %w", sf.Name, err)
		}
	}
	return nil
}

func sortSchemaFile(entries []schemaFile) {
	sort.Slice(entries, func(i, j int) bool {
		fi := entries[i]
		fj := entries[j]
		return VerAfter(fj.Name, fi.Name)
	})
}

type schemaFile struct {
	Name string
	Path string
	Fs   ReadFS
}

func (sf schemaFile) Read() ([]byte, error) {
	buf, err := sf.Fs.ReadFile(sf.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to fs.ReadFile, %v, %w", sf.Path, err)
	}
	return buf, nil
}

func convertSchemaFiles(last string, files []fs.DirEntry, baseDir string, fs ReadFS) []schemaFile {
	filtered := make([]schemaFile, 0, len(files))
	for _, f := range files {
		if !f.Type().IsRegular() {
			continue
		}
		name := strings.ToLower(f.Name())
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		if last != "" && !VerAfter(name, last) {
			continue
		}
		filtered = append(filtered, schemaFile{
			Name: name,
			Path: baseDir + "/" + name,
			Fs:   fs,
		})
	}
	return filtered
}

type schemaVersion struct {
	Id      int64
	Script  string
	Success bool
	Remark  string
}

func runSQLFile(db *gorm.DB, log Logger, app string, content []byte, fname string) error {
	contentStr := string(content)
	segments := strings.Split(contentStr, ";")

	total := 0
	for i, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if err := db.Exec(seg).Error; err != nil {
			if er := saveSchemaVer(db, app, fname, false, err.Error()); er != nil {
				log.Errorf("failed to save schema_version, %w", er)
			}
			return fmt.Errorf("failed to execute script, the %v segments, '%v', %w", i+1, seg, err)
		} else {
			log.Infof("'%v' - executed [%v]: \n\n%v\n", fname, i+1, seg)
		}
		total += 1
	}
	log.Infof("Script %v completed", fname)

	if er := saveSchemaVer(db, app, fname, true, fmt.Sprintf("Executed %d SQLs", total)); er != nil {
		log.Errorf("failed to save schema_version, %v, %w", fname, er)
	}
	return nil
}

func saveSchemaVer(db *gorm.DB, app string, script string, success bool, remark string) error {
	rrm := []rune(remark)
	if len(rrm) > 255 {
		rrm = rrm[:255]
	}
	return db.Exec(`INSERT INTO schema_version (app, script, success, remark) VALUES (?,?,?,?)`,
		app, script, success, string(rrm)).Error
}
