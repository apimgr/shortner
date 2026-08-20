package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// SecurityReport is one coordinated-disclosure submission, per AI.md
// PART 11 "Security Reports". Sealed holds the encrypted report body;
// every other field is triage metadata safe to keep in the clear.
type SecurityReport struct {
	TrackingID string
	ReceivedAt time.Time
	Severity   string
	Component  string
	Status     string
	Sealed     string
}

// NewTrackingID allocates the `sec_` + 16 random hex chars identifier
// AI.md PART 11 "Submission Flow" step 2 prescribes.
func NewTrackingID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("db: tracking id: %w", err)
	}
	return "sec_" + hex.EncodeToString(buf), nil
}

// InsertSecurityReport persists a submitted report. The caller must have
// already encrypted rep.Sealed — this function never sees plaintext.
func InsertSecurityReport(ctx context.Context, sqlDB *sql.DB, rep SecurityReport) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO security_reports (tracking_id, received_at, severity, component, status, sealed)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		rep.TrackingID, rep.ReceivedAt.Unix(), rep.Severity, rep.Component, "received", rep.Sealed)
	if err != nil {
		return HandleQueryError(err)
	}
	return nil
}

// GetSecurityReport returns a single report by tracking id. Returns
// ErrNotFound when no such report exists.
func GetSecurityReport(ctx context.Context, sqlDB *sql.DB, trackingID string) (*SecurityReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var (
		rep      SecurityReport
		received int64
	)
	err := sqlDB.QueryRowContext(ctx,
		`SELECT tracking_id, received_at, severity, component, status, sealed
		   FROM security_reports WHERE tracking_id = ?`, trackingID).
		Scan(&rep.TrackingID, &received, &rep.Severity, &rep.Component, &rep.Status, &rep.Sealed)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	rep.ReceivedAt = time.Unix(received, 0).UTC()
	return &rep, nil
}
