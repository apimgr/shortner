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
	TrackingID   string
	ReceivedAt   time.Time
	UpdatedAt    time.Time
	Severity     string
	Component    string
	Status       string
	Sealed       string
	ClosedAt     time.Time
	LastViewedAt time.Time
	DisclosureAt time.Time
	Comments     string
}

// SecurityReportUpdate contains operator supplied fields for one report.
type SecurityReportUpdate struct {
	Status       *string
	Comments     *string
	DisclosureAt *time.Time
}

const (
	StatusReceived  = "received"
	StatusTriaged   = "triaged"
	StatusConfirmed = "confirmed"
	StatusPatching  = "patching"
	StatusDisclosed = "disclosed"
	StatusWontFix   = "wont_fix"
)

// IsSecurityReportStatus reports whether status is one of the public report
// states supported by AI.md PART 11.
func IsSecurityReportStatus(status string) bool {
	switch status {
	case StatusReceived, StatusTriaged, StatusConfirmed, StatusPatching, StatusDisclosed, StatusWontFix:
		return true
	default:
		return false
	}
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
func InsertSecurityReport(ctx context.Context, sqlDB Handle, rep SecurityReport) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO security_reports (tracking_id, received_at, updated_at, severity, component, status, sealed, closed_at, last_viewed_at, disclosure_at, comments)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rep.TrackingID, rep.ReceivedAt.Unix(), rep.ReceivedAt.Unix(), rep.Severity, rep.Component, StatusReceived, rep.Sealed,
		unixOrZero(rep.ClosedAt), unixOrZero(rep.LastViewedAt), unixOrZero(rep.DisclosureAt), rep.Comments)
	if err != nil {
		return HandleQueryError(err)
	}
	return nil
}

// GetSecurityReport returns a single report by tracking id. Returns
// ErrNotFound when no such report exists.
func GetSecurityReport(ctx context.Context, sqlDB Handle, trackingID string) (*SecurityReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return scanSecurityReport(sqlDB.QueryRowContext(ctx,
		`SELECT tracking_id, received_at, updated_at, severity, component, status, sealed, closed_at, last_viewed_at, disclosure_at, comments
		   FROM security_reports WHERE tracking_id = ?`, trackingID))
}

func scanSecurityReport(row interface{ Scan(...any) error }) (*SecurityReport, error) {
	var (
		rep                                             SecurityReport
		received, updated, closed, viewed, disclosureAt int64
	)
	err := row.Scan(&rep.TrackingID, &received, &updated, &rep.Severity, &rep.Component, &rep.Status, &rep.Sealed,
		&closed, &viewed, &disclosureAt, &rep.Comments)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	rep.ReceivedAt = time.Unix(received, 0).UTC()
	rep.UpdatedAt = time.Unix(updated, 0).UTC()
	rep.ClosedAt = timeFromUnixOrZero(closed)
	rep.LastViewedAt = timeFromUnixOrZero(viewed)
	rep.DisclosureAt = timeFromUnixOrZero(disclosureAt)
	return &rep, nil
}

// ListSecurityReports returns triage metadata only, optionally filtered by status.
func ListSecurityReports(ctx context.Context, sqlDB Handle, status string) ([]SecurityReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query := `SELECT tracking_id, received_at, updated_at, severity, component, status FROM security_reports`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY received_at DESC, tracking_id`
	rows, err := sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	defer rows.Close()
	var reports []SecurityReport
	for rows.Next() {
		var rep SecurityReport
		var received, updated int64
		if err := rows.Scan(&rep.TrackingID, &received, &updated, &rep.Severity, &rep.Component, &rep.Status); err != nil {
			return nil, HandleQueryError(err)
		}
		rep.ReceivedAt, rep.UpdatedAt = time.Unix(received, 0).UTC(), time.Unix(updated, 0).UTC()
		reports = append(reports, rep)
	}
	if err := rows.Err(); err != nil {
		return nil, HandleQueryError(err)
	}
	return reports, nil
}

// UpdateSecurityReport applies metadata changes while enforcing forward-only transitions.
func UpdateSecurityReport(ctx context.Context, sqlDB Handle, trackingID string, update SecurityReportUpdate, now time.Time) (*SecurityReport, string, error) {
	var currentStatus string
	var updated *SecurityReport
	err := WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		current, err := scanSecurityReport(tx.QueryRowContext(ctx,
			`SELECT tracking_id, received_at, updated_at, severity, component, status, sealed, closed_at, last_viewed_at, disclosure_at, comments
			   FROM security_reports WHERE tracking_id = ?`, trackingID))
		if err != nil {
			return err
		}
		currentStatus = current.Status
		newStatus := current.Status
		if update.Status != nil {
			if !IsSecurityReportStatus(*update.Status) {
				return fmt.Errorf("db: invalid security report status %q", *update.Status)
			}
			if !securityStatusForward(current.Status, *update.Status) {
				return fmt.Errorf("db: security report status cannot move from %q to %q", current.Status, *update.Status)
			}
			newStatus = *update.Status
		}
		comments := current.Comments
		if update.Comments != nil {
			if len([]rune(*update.Comments)) > 2000 {
				return fmt.Errorf("db: security report comment exceeds 2000 characters")
			}
			comments = *update.Comments
		}
		disclosure := current.DisclosureAt
		if update.DisclosureAt != nil {
			disclosure = update.DisclosureAt.UTC()
		}
		closed := current.ClosedAt
		if newStatus == StatusDisclosed || newStatus == StatusWontFix {
			if closed.IsZero() {
				closed = now.UTC()
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE security_reports SET status=?, comments=?, disclosure_at=?, closed_at=?, updated_at=? WHERE tracking_id=?`, newStatus, comments, unixOrZero(disclosure), unixOrZero(closed), unixOrZero(now), trackingID); err != nil {
			return HandleQueryError(err)
		}
		updated, err = scanSecurityReport(tx.QueryRowContext(ctx,
			`SELECT tracking_id, received_at, updated_at, severity, component, status, sealed, closed_at, last_viewed_at, disclosure_at, comments
			   FROM security_reports WHERE tracking_id = ?`, trackingID))
		return err
	})
	return updated, currentStatus, err
}

func securityStatusForward(old, next string) bool {
	if old == next {
		return true
	}
	if old == StatusDisclosed || old == StatusWontFix {
		return false
	}
	if next == StatusWontFix {
		return true
	}
	rank := map[string]int{StatusReceived: 0, StatusTriaged: 1, StatusConfirmed: 2, StatusPatching: 3, StatusDisclosed: 4}
	return rank[next] > rank[old]
}

// TryTouchSecurityReportView atomically claims a one-shot status-page view.
// A concurrent request cannot pass the daily interval check after another
// request has already consumed the view token.
func TryTouchSecurityReportView(ctx context.Context, sqlDB Handle, trackingID string, viewedAt time.Time, interval time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	now := viewedAt.UTC().Unix()
	cutoff := now - int64(interval/time.Second)
	result, err := sqlDB.ExecContext(ctx,
		`UPDATE security_reports SET last_viewed_at = ? WHERE tracking_id = ? AND (last_viewed_at = 0 OR last_viewed_at <= ?)`,
		now, trackingID, cutoff)
	if err != nil {
		return false, HandleQueryError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("db: security report view rows affected: %w", err)
	}
	return rows == 1, nil
}

// TouchSecurityReportView records the last time a researcher opened their
// status page. This unconditional setter is intended for maintenance and tests;
// request handlers should use TryTouchSecurityReportView.
func TouchSecurityReportView(ctx context.Context, sqlDB Handle, trackingID string, viewedAt time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := sqlDB.ExecContext(ctx,
		`UPDATE security_reports SET last_viewed_at = ? WHERE tracking_id = ?`,
		unixOrZero(viewedAt), trackingID)
	if err != nil {
		return HandleQueryError(err)
	}
	return nil
}

func unixOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().Unix()
}

func timeFromUnixOrZero(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}
