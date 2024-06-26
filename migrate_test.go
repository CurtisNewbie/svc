package svc

import (
	"embed"
	"fmt"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

//go:embed schema/svc/*.sql
var schemaFs embed.FS

func TestMigrate(t *testing.T) {
	user := "root"
	pw := ""
	host := "localhost"
	port := 3306
	schema := "tt"
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s%s", user, pw, host, port, schema, "")

	conn, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn = conn.Debug()

	conf := MigrateConfig{
		App:     "test",
		Fs:      schemaFs,
		BaseDir: "schema/svc",
	}

	// conn = conn.Debug()
	err = MigrateSchema(conn, PrintLogger{}, conf)
	if err != nil {
		t.Fatal(err)
	}
}
