package svc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

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

	// optional, if provided, svc starts with the provided version, if absent, svc follows previous schema script execution.
	StartingVersion string
}

func MigrateSchema(db *gorm.DB, log Logger, c MigrateConfig) error {
	if c.Fs == nil {
		return errors.New("fs is nil")
	}
	if log == nil {
		return errors.New("log is nil")
	}
	if db == nil {
		return errors.New("db is nil")
	}

	err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
			app VARCHAR(50) NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			script VARCHAR(256) NOT NULL DEFAULT '',
			success TINYINT(1) NOT NULL DEFAULT 1,
			remark VARCHAR(256) NOT NULL DEFAULT '',
			PRIMARY KEY (id),
			KEY app_idx (app)
		)
	`).Error
	if err != nil {
		return fmt.Errorf("failed to create schema_verion table, %w", err)
	}

	var last string
	if c.StartingVersion != "" {
		last = c.StartingVersion
	} else {
		lastVer := new(schemaVersion)
		t := db.Raw(`
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
		if lastVer != nil {
			last = lastVer.Script
		}
	}

	files, err := c.Fs.ReadDir(c.BaseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to open %v folders, %w", c.BaseDir, err)
	}

	sort.Slice(files, func(i, j int) bool {
		fi := files[i]
		fj := files[j]
		return VerAfter(fj.Name(), fi.Name())
	})

	for _, f := range files {
		if !f.Type().IsRegular() {
			continue
		}
		name := strings.ToLower(f.Name())
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		// log.Infof("curr: %v, last: %v", name, last)
		if last != "" && !VerAfter(name, last) {
			continue
		}
		fpath := c.BaseDir + "/" + name
		content, err := c.Fs.ReadFile(fpath)
		if err != nil {
			return fmt.Errorf("failed to fs.ReadFile, %v, %w", fpath, err)
		}

		if err := runSQLFile(db, log, c.App, content, name); err != nil {
			return fmt.Errorf("failed to exec sql file %v, %w", name, err)
		}
		last = name
	}
	return nil
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
			log.Infof("'%v' - executed [%v]: \n\n%v\n\n", fname, i+1, seg)
		}
	}
	log.Infof("Script %v completed", fname)

	if er := saveSchemaVer(db, app, fname, true, ""); er != nil {
		log.Errorf("failed to save schema_version, %w", er)
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
