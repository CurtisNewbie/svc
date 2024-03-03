# svc

svc stands for schema version control, a simple tool to manage schema migration.

## How svc works

svc first tries to create a table to maintain the scripts it executed:

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
);
```

Everytime svc runs, it queries the last execution log from the `schema_version` table. If the last execution was failed (`success=0`),
svc returns error until the error and the record is fixed manually.

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