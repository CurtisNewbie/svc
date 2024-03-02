package svc

import (
	"testing"
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
