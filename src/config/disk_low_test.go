package config

import "testing"

func TestValidateDiskLowNotification(t *testing.T) {
	cfg := Default("")
	cfg.Server.Notifications.DiskLow.ThresholdPercent = 101
	cfg.Server.Notifications.DiskLow.CheckInterval = "bad"
	cfg.Server.Notifications.DiskLow.Repeat = "0s"
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings for invalid disk_low settings")
	}
	got := cfg.Server.Notifications.DiskLow
	if got.ThresholdPercent != 85 || got.CheckInterval != "5m" || got.Repeat != "24h" {
		t.Fatalf("disk_low after validation = %#v", got)
	}
}
