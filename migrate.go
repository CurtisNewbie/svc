package svc

import (
	"embed"
	"fmt"
	"os"
	"sort"
	"strings"

	"gorm.io/gorm"
)

const (
	baseDir = "schema/svc"
)

//go:embed schema/svc/*.sql
var schemaFs embed.FS

type SchemaVersion struct {
	Id      int64
	Version string
	Success bool
	Remark  string
}

func MigrateSchema(db *gorm.DB, log Logger) error {
	err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			version VARCHAR(256) NOT NULL DEFAULT '',
			success TINYINT(1) NOT NULL DEFAULT 1,
			remark VARCHAR(256) NOT NULL DEFAULT '',
			PRIMARY KEY (id)
		)
	`).Error
	if err != nil {
		return fmt.Errorf("failed to create schema_verion table, %w", err)
	}

	lastVer := new(SchemaVersion)
	t := db.Raw(`SELECT id, version, success, remark FROM schema_version ORDER BY id DESC LIMIT 1`).Scan(lastVer)
	if t.Error != nil {
		return fmt.Errorf("failed to list schema_verion, %w", t.Error)
	}
	if t.RowsAffected < 1 {
		lastVer = nil
	} else if !lastVer.Success {
		return fmt.Errorf("previous schema migration was failed, last attempt was '%v' (%v), please fix the execution manually and update the last 'schema_version' record status (id: %v)", lastVer.Version, lastVer.Remark, lastVer.Id)
	}

	files, err := schemaFs.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to open %v folders, %w", baseDir, err)
	}

	sort.Slice(files, func(i, j int) bool {
		fi := files[i]
		fj := files[j]
		return VerAfter(fj.Name(), fi.Name())
	})

	var last string
	if lastVer != nil {
		last = lastVer.Version
	}
	for _, f := range files {
		fn := f.Name()
		if last != "" && !VerAfter(fn, last) {
			// log.Infof("Script %v has been executed, skipped, last one is %v", fn, last)
			continue
		}
		if err := RunSQLFile(db, log, schemaFs, baseDir+"/"+fn, fn); err != nil {
			return fmt.Errorf("failed to exec sql file %v, %w", fn, err)
		}
		last = fn
	}
	return nil
}

func RunSQLFile(db *gorm.DB, log Logger, fs embed.FS, fname string, ver string) error {
	content, err := fs.ReadFile(fname)
	if err != nil {
		return fmt.Errorf("failed to fs.ReadFile, %v, %w", fname, err)
	}
	contentStr := string(content)
	segments := strings.Split(contentStr, ";")

	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if err := db.Exec(seg).Error; err != nil {
			if er := saveSchemaVer(db, ver, false, err.Error()); er != nil {
				log.Errorf("failed to save schema_version, %w", er)
			}
			return fmt.Errorf("failed to execute script, '%v', %w", seg, err)
		}
	}
	log.Infof("Script %v executed", fname)

	if er := saveSchemaVer(db, ver, true, ""); er != nil {
		log.Errorf("failed to save schema_version, %w", er)
	}
	return nil
}

func saveSchemaVer(db *gorm.DB, ver string, success bool, remark string) error {
	rrm := []rune(remark)
	if len(rrm) > 255 {
		rrm = rrm[:255]
	}
	return db.Exec(`INSERT INTO schema_version (version, success, remark) VALUES (?,?,?)`, ver, success, string(rrm)).Error
}
