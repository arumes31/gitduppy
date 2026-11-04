package gitops

import (
	"testing"
	"time"
)

func TestAuditLogCutoff(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	if _, ok := auditLogCutoff(now, 0); ok {
		t.Error("retention 0 must keep audit logs forever")
	}
	got, ok := auditLogCutoff(now, 90*24*time.Hour)
	if !ok || !got.Equal(now.AddDate(0, 0, -90)) {
		t.Errorf("cutoff = %v (%v), want %v", got, ok, now.AddDate(0, 0, -90))
	}
}

func TestDefaultCleanupConfigKeepsAuditLogsLonger(t *testing.T) {
	cfg := DefaultCleanupConfig()
	if cfg.AuditRetention != 90*24*time.Hour {
		t.Errorf("default audit retention = %v, want 90 days", cfg.AuditRetention)
	}
	if w := NewCleanupWorker(&CleanupConfig{Interval: time.Hour, Retention: time.Hour, AuditRetention: 5 * time.Hour}); w.auditRetention != 5*time.Hour {
		t.Errorf("audit retention not wired into worker: %v", w.auditRetention)
	}
}
