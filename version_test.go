package svc

import (
	"fmt"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPadVer(t *testing.T) {
	v := []string{"1"}
	v = PadVer(v, 3)
	if len(v) != 3 {
		t.Fatal("should be 3")
	}
	for i, s := range v {
		if i == 0 && s != "1" {
			t.Fatal("should be 1")
		}
		if i > 0 && s != "0" {
			t.Fatal("should be 0")
		}
	}
}

func TestVerAfter(t *testing.T) {
	if VerAfter("v1.1.3.4.sql", "v2.0.3.sql") {
		t.Fatal("should return false")
	}
	if !VerAfter("v2", "v1.2.3") {
		t.Fatal("should return true")
	}
	if !VerAfter("2.1", "1") {
		t.Fatal("should return true")
	}
}

func TestMigrate(t *testing.T) {
	user := "root"
	pw := ""
	host := "localhost"
	port := 3306
	schema := "vfm"
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s%s", user, pw, host, port, schema, "")

	conn, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	err = MigrateSchema(conn.Debug(), PrintLogger{})
	if err != nil {
		t.Fatal(err)
	}
}
