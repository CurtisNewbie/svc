# svc

svc stands for schema version control, a simple tool to manage schema migration.

## How svc works

svc first tries to create a few table to maintain the scripts it executed:

```sql
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

CREATE TABLE IF NOT EXISTS schema_script_sql (
    id BIGINT(20) UNSIGNED NOT NULL AUTO_INCREMENT,
    app VARCHAR(50) NOT NULL DEFAULT '',
    script VARCHAR(256) NOT NULL DEFAULT '',
    sql_script TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY app_idx (app, script)
) ENGINE=INNODB DEFAULT CHARSET=utf8mb4 comment='svc schema script sqls';
```

Everytime svc runs, it queries the last execution log from the `schema_version` table. If the last execution was failed (`success=0`),
svc returns error until the error and the record are fixed manually.

e.g.,

```sql
UPDATE schema_version SET success=1 WHERE id = ?;
```

If last execution was success, it loads all scripts files from the provided `Fs` object. The scripts files are sorted and executed one by one only if svc considers the script file belongs to a version that is after the last execution.

For example, the last executed file is 'v0.0.2.sql'. From the provided FS, svc found following files:

```
- v0.0.1.sql
- v0.0.2.sql
- v0.0.3.sql
```

svc recognizes that both v0.0.1.sql and v0.0.2.sql belong to a version that is before 0.0.2. These two files are simply ignored. Then only the v0.0.3.sql file is executed.

The entry point of svc is:

```go
func MigrateSchema(db *gorm.DB, log Logger, c MigrateConfig) error {
    // ...
}
```

e.g., we may write code like the following

```go
//go:embed schema/*.sql
var schemaFs embed.FS

conf := MigrateConfig{
    App:     "test",
    Fs:      schemaFs,
    BaseDir: "schema",
}
err = MigrateSchema(conn.Debug(), PrintLogger{}, conf)

// ...
```

Then in database:

```
> select * from schema_version;

+----+------+---------------------+------------+---------+--------+
| id | app  | created_at          | script     | success | remark |
+----+------+---------------------+------------+---------+--------+
| 30 | test | 2024-03-02 18:45:46 | v0.0.1.sql |       1 |        |
| 31 | test | 2024-03-02 18:45:46 | v0.0.2.sql |       1 |        |
+----+------+---------------------+------------+---------+--------+
```

**What if the application was newly installed, and there are several SQLs files managed by svc (e.g., v0.0.1.sql, v0.0.2.sql)?**

In this case, we know that the application is already using the latest version, we shouldn't execute any SQL scripts at all. svc handles this by checking whether the `schema_version` table exists; if not, svc knows that we are already at the latest version, and it inserts a `schema_version` record with the last script name, pretending that we have migrated to that last version.

**What if the last SQL file was modified after the migration?**

In general, these migration SQL scripts should never be modified. But svc does try to examine whether new SQLs statements were added into the last SQL script that has already been executed.

E.g., we have v0.0.1.sql and v0.0.2.sql, and we have already migrated to v0.0.2.sql.

In v0.0.2.sql, we have the following:

```sql
SELECT 1;
```

If we add a new SQL statement to the script as the following:

```sql
SELECT 1;

SELECT 2;
```

svc indeed will execute the newly added SQL `SELECT 2;`, the SQLs statements that are executed, are saved in table `schema_script_sql`. However, this functionality is mainly used for development.