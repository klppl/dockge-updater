package app

import (
	"testing"
	"time"
)

func TestValidateSettings(t *testing.T) {
	valid := DefaultSettings()
	valid.AutoUpdatePolicy = "weekly"
	if err := validateSettings(valid); err != nil {
		t.Fatalf("default-derived settings should be valid: %v", err)
	}

	invalid := valid
	invalid.CheckTime = "3:00"
	if err := validateSettings(invalid); err == nil {
		t.Fatal("expected non-padded time to fail")
	}
	invalid = valid
	invalid.AutoUpdatePolicy = "sometimes"
	if err := validateSettings(invalid); err == nil {
		t.Fatal("expected unknown policy to fail")
	}
}

func TestNextDaily(t *testing.T) {
	location := time.FixedZone("test", 2*60*60)
	now := time.Date(2026, time.September, 6, 2, 30, 0, 0, location)
	got := nextDaily(now, "03:00")
	want := time.Date(2026, time.September, 6, 3, 0, 0, 0, location)
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}

	after := time.Date(2026, time.September, 6, 3, 1, 0, 0, location)
	got = nextDaily(after, "03:00")
	want = time.Date(2026, time.September, 7, 3, 0, 0, 0, location)
	if !got.Equal(want) {
		t.Fatalf("expected next day %v, got %v", want, got)
	}
}
