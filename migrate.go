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

var (
	excluded = map[string]struct{}{}
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

	// check if the table doesn't exist at all
	// for the first time we run svc, we know that we don't need to migrate
	// schema, the schema we have is already the latest version
	var firstRun = false
	if err := db.Exec(`SELECT id FROM schema_version LIMIT 1`).Error; err != nil {
		firstRun = true
		log.Infof("schema_version not exists, initializing schema_version to latest one")
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
	) ENGINE=INNODB DEFAULT CHARSET=utf8mb4 comment='svc schema version';
	`)
	if t.Error != nil {
		return fmt.Errorf("failed to create schema_verion table, %w", t.Error)
	}

	t = db.Exec(`
	CREATE TABLE IF NOT EXISTS schema_script_sql (
		id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
		app VARCHAR(50) NOT NULL DEFAULT '',
		script VARCHAR(256) NOT NULL DEFAULT '',
		sql_script TEXT,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (id),
		KEY app_idx (app, script)
	) ENGINE=INNODB DEFAULT CHARSET=utf8mb4 comment='svc schema script sqls';
	`)
	if t.Error != nil {
		return fmt.Errorf("failed to create schema_script_sql table, %w", t.Error)
	}

	var last string
	if c.StartingVersion != "" {
		last = c.StartingVersion
	}

	lastVer := new(schemaVersion)
	if !firstRun {
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
			return fmt.Errorf(`previous schema migration was failed, last attempt was '%v' (%v), please fix the execution
 manually and update the last 'schema_version' record status (id: %v)`,
				lastVer.Script, lastVer.Remark, lastVer.Id)
		}
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

	schemaFiles, err := convertSchemaFiles(last, files, c.BaseDir, c.Fs)
	if err != nil {
		return err
	}
	sortSchemaFile(schemaFiles)

	if firstRun && len(schemaFiles) > 0 {
		last := schemaFiles[len(schemaFiles)-1]
		if er := saveSchemaVer(db, c.App, last.Name, true, fmt.Sprintf("Initialized at version %v", last.Name)); er != nil {
			log.Errorf("failed to save schema_version, %v, %w", last.Name, er)
			return err
		}
		return nil
	}

	for i, sf := range schemaFiles {

		// for the last one, check whether there are new sqls being added to the script file (e.g., during development)
		if i == len(schemaFiles)-1 {
			var executed []string
			if err := db.Raw(`SELECT sql_script FROM schema_script_sql WHERE app = ? and script = ?`, c.App, sf.Name).Scan(&executed).Error; err != nil {
				return err
			}

			// start filtering
			if len(executed) > 0 {
				mem := map[string]struct{}{}
				for _, s := range executed {
					mem[s] = struct{}{}
				}

				sqls := make([]string, 0, len(sf.SQLs))
				for _, s := range sf.SQLs {
					if _, ok := mem[s]; ok {
						continue
					}
					sqls = append(sqls, s)
				}
				sf.SQLs = sqls
			} else if VerEq(sf.Name, last) {
				// schema_script_sql is emtpy, and the version is equal,
				// we should just skip the script, the script has been executed already,
				// before the newly created schema_script_sql.
				continue
			}
		} else if VerEq(sf.Name, last) {
			// not the last script, and the version is equal, skip
			continue
		}

		if err := runSQLFile(db, log, c.App, sf.SQLs, sf.Name); err != nil {
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
	SQLs []string
}

func convertSchemaFiles(last string, files []fs.DirEntry, baseDir string, fs ReadFS) ([]schemaFile, error) {
	filtered := make([]schemaFile, 0, len(files))
	for _, f := range files {
		if !f.Type().IsRegular() {
			continue
		}
		name := strings.ToLower(f.Name())
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		if isExcluded(name) {
			continue
		}

		if last != "" && !VerAfterEq(name, last) {
			continue
		}

		path := baseDir + "/" + name
		buf, err := fs.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to fs.ReadFile, %v, %w", path, err)
		}

		contentStr := string(buf)
		segments := strings.Split(contentStr, ";")

		sqls := []string{}
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			sqls = append(sqls, seg)
		}
		if len(sqls) < 1 {
			continue
		}

		filtered = append(filtered, schemaFile{
			Name: name,
			Path: path,
			SQLs: sqls,
		})
	}
	return filtered, nil
}

type schemaVersion struct {
	Id      int64
	Script  string
	Success bool
	Remark  string
}

func runSQLFile(db *gorm.DB, log Logger, app string, segments []string, fname string) error {
	total := 0
	for i, sql := range segments {

		// record that we have executed the sql regardless of whether it will succeed or not.
		if err := db.Exec(`INSERT INTO schema_script_sql (app, script, sql_script) VALUES (?,?,?)`, app, fname, sql).Error; err != nil {
			return fmt.Errorf("failed to save schema_script_sql, %v", err)
		}

		if err := db.Exec(sql).Error; err != nil {
			if er := saveSchemaVer(db, app, fname, false, err.Error()); er != nil {
				log.Errorf("failed to save schema_version, %v", er)
			}
			return fmt.Errorf("failed to execute script, '%v', %w", sql, err)
		} else {
			log.Infof("'%v' - executed [%v]: \n\n%v\n", fname, i+1, sql)
		}
		total += 1
	}
	log.Infof("Script %v completed", fname)

	if er := saveSchemaVer(db, app, fname, true, "Executed"); er != nil {
		log.Errorf("failed to save schema_version, %v, %v", fname, er)
	}
	return nil
}

func saveSchemaVer(db *gorm.DB, app string, script string, success bool, remark string) error {
	rrm := []rune(remark)
	if len(rrm) > 255 {
		rrm = rrm[:255]
	}

	// update schema_verion
	var id int
	t := db.Raw(`SELECT id FROM schema_version WHERE app = ? and script = ? LIMIT 1`, app, script).Scan(&id)
	if err := t.Error; err != nil {
		return err
	}
	if t.RowsAffected > 0 {
		return db.Exec(`UPDATE schema_version SET success = ?, remark = ? WHERE id = ?`, success, string(rrm), id).Error
	}

	// save new schema_verion
	return db.Exec(`INSERT INTO schema_version (app, script, success, remark) VALUES (?,?,?,?)`,
		app, script, success, string(rrm)).Error
}

func ExcludeFile(name string) {
	excluded[name] = struct{}{}
}

func isExcluded(name string) bool {
	name = strings.ToLower(name)
	_, ok := excluded[name]
	return ok
}
