package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/pgp"
)

// SecurityReport is one coordinated-disclosure submission, per AI.md
// PART 11 "Security Reports". Sealed holds the encrypted report body;
// every other field is triage metadata safe to keep in the clear.
type SecurityReport struct {
	TrackingID        string
	SecurityID        string
	CreatedAt         time.Time
	ReceivedAt        time.Time
	UpdatedAt         time.Time
	Severity          string
	Component         string
	Status            string
	Sealed            string
	BodyEnc           []byte
	BodyAlg           string
	ContactEnc        []byte
	CreditPref        string
	CreditName        string
	ResearcherComment string
	CVERequested      bool
	TokenHash         string
	TokenLastUsed     time.Time
	ClosedAt          time.Time
	LastViewedAt      time.Time
	DisclosureAt      time.Time
	Comments          string
}

// SecurityReportUpdate contains operator supplied fields for one report.
type SecurityReportUpdate struct {
	Status       *string
	Comments     *string
	DisclosureAt *time.Time
	Severity     *string
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

// IsSecurityReportSeverity reports whether severity is one of the PART 11
// values stored in lowercase.
func IsSecurityReportSeverity(severity string) bool {
	switch severity {
	case "critical", "high", "medium", "low", "informational":
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

	created := rep.CreatedAt
	if created.IsZero() {
		created = rep.ReceivedAt
	}
	body := rep.BodyEnc
	if len(body) == 0 {
		body = []byte(rep.Sealed)
	}
	bodyAlg := rep.BodyAlg
	if bodyAlg == "" {
		bodyAlg = "aes-256-gcm"
	}
	researcherComment := rep.ResearcherComment
	if researcherComment == "" {
		researcherComment = rep.Comments
	}
	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO security_reports (tracking_id, security_id, received_at, created_at, updated_at, severity, component, status, body_enc, body_alg, contact_enc, credit_pref, credit_name, researcher_comment, disclosure_date, cve_requested, token_hash, closed_at, last_viewed_at, disclosure_at, comments)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rep.TrackingID, rep.SecurityID, created.Unix(), created.Unix(), created.Unix(), rep.Severity, rep.Component, StatusReceived, body, bodyAlg,
		rep.ContactEnc, defaultCreditPref(rep.CreditPref), nullString(rep.CreditName), researcherComment, nullableDate(rep.DisclosureAt), rep.CVERequested, rep.TokenHash,
		unixOrZero(rep.ClosedAt), unixOrZero(rep.LastViewedAt), unixOrZero(rep.DisclosureAt), researcherComment)
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
	return scanSecurityReport(sqlDB.QueryRowContext(ctx, securityReportSelect+` WHERE tracking_id = ?`, trackingID))
}

const securityReportSelect = `SELECT tracking_id, security_id, created_at, updated_at, severity, component, status, body_enc, body_alg, contact_enc, credit_pref, credit_name, researcher_comment, disclosure_date, cve_requested, token_hash, token_last_used, closed_at, last_viewed_at FROM security_reports`

func scanSecurityReport(row interface{ Scan(...any) error }) (*SecurityReport, error) {
	var (
		rep                                       SecurityReport
		created, updated, closed, viewed          int64
		body                                      []byte
		creditName, disclosureDate, tokenLastUsed sql.NullString
		cveRequested                              int
	)
	err := row.Scan(&rep.TrackingID, &rep.SecurityID, &created, &updated, &rep.Severity, &rep.Component, &rep.Status, &body, &rep.BodyAlg,
		&rep.ContactEnc, &rep.CreditPref, &creditName, &rep.ResearcherComment, &disclosureDate, &cveRequested, &rep.TokenHash, &tokenLastUsed, &closed, &viewed)
	if err != nil {
		return nil, HandleQueryError(err)
	}
	rep.Sealed = string(body)
	rep.BodyEnc = append([]byte(nil), body...)
	rep.CreatedAt = time.Unix(created, 0).UTC()
	rep.ReceivedAt = rep.CreatedAt
	rep.UpdatedAt = time.Unix(updated, 0).UTC()
	rep.CreditName = creditName.String
	rep.CVERequested = cveRequested != 0
	rep.Comments = rep.ResearcherComment
	if disclosureDate.Valid {
		rep.DisclosureAt, _ = time.Parse("2006-01-02", disclosureDate.String)
		rep.DisclosureAt = rep.DisclosureAt.UTC()
	}
	if tokenLastUsed.Valid {
		rep.TokenLastUsed, _ = time.Parse("2006-01-02", tokenLastUsed.String)
		rep.TokenLastUsed = rep.TokenLastUsed.UTC()
	}
	rep.ClosedAt = timeFromUnixOrZero(closed)
	rep.LastViewedAt = timeFromUnixOrZero(viewed)
	return &rep, nil
}

// ListSecurityReports returns triage metadata only, optionally filtered by status.
func ListSecurityReports(ctx context.Context, sqlDB Handle, status string) ([]SecurityReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query := `SELECT tracking_id, created_at, updated_at, severity, component, status FROM security_reports`
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

// DeleteClosedSecurityReportsBefore removes reports closed at or before the
// retention cutoff. Open reports and the independent security_thanks rows
// are never deleted, per AI.md PART 11.
func DeleteClosedSecurityReportsBefore(ctx context.Context, sqlDB Handle, cutoff time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := sqlDB.ExecContext(ctx, `DELETE FROM security_reports WHERE closed_at > 0 AND closed_at <= ?`, cutoff.UTC().Unix())
	if err != nil {
		return 0, HandleQueryError(err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("db: security report cleanup rows affected: %w", err)
	}
	return deleted, nil
}

// RotateSecurityReportPGP re-encrypts every retained PGP report and updates
// the active key metadata in the same database transaction. The caller must
// hold the PGP store's exclusive key lock so report submission cannot race.
func RotateSecurityReportPGP(ctx context.Context, sqlDB Handle, oldKey, newKey *pgp.Keypair) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if oldKey == nil || newKey == nil {
		return fmt.Errorf("db: PGP rotation requires both keys")
	}
	armored, err := newKey.ArmorPublic()
	if err != nil {
		return err
	}
	recipients, err := pgp.ParsePublic(armored)
	if err != nil {
		return err
	}
	return WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT tracking_id, body_enc FROM security_reports WHERE body_alg = 'pgp'`)
		if err != nil {
			return HandleQueryError(err)
		}
		type encryptedReport struct {
			trackingID string
			body       string
		}
		reports := make([]encryptedReport, 0)
		for rows.Next() {
			var report encryptedReport
			var body []byte
			if err := rows.Scan(&report.trackingID, &body); err != nil {
				_ = rows.Close()
				return err
			}
			report.body = string(body)
			reports = append(reports, report)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, report := range reports {
			plain, err := pgp.DecryptArmored(oldKey, report.body)
			if err != nil {
				return fmt.Errorf("db: decrypt PGP report %s: %w", report.trackingID, err)
			}
			sealed, err := pgp.EncryptArmored(recipients, plain)
			if err != nil {
				return fmt.Errorf("db: encrypt PGP report %s: %w", report.trackingID, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE security_reports SET body_enc = ? WHERE tracking_id = ? AND body_alg = 'pgp'`, []byte(sealed), report.trackingID); err != nil {
				return HandleQueryError(err)
			}
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO pgp_keys (fingerprint, created_at, expires_at, last_rotated_at, keyservers_published, revoked)
			 VALUES (?, ?, ?, ?, '', 0)
			 ON CONFLICT(fingerprint) DO UPDATE SET created_at=excluded.created_at, expires_at=excluded.expires_at, last_rotated_at=excluded.last_rotated_at, revoked=0`,
			strings.ToUpper(newKey.Fingerprint), newKey.CreatedAt.UTC().Unix(), newKey.ExpiresAt.UTC().Unix(), newKey.CreatedAt.UTC().Unix())
		return HandleQueryError(err)
	})
}

// UpdateSecurityReport applies metadata changes while enforcing forward-only transitions.
func UpdateSecurityReport(ctx context.Context, sqlDB Handle, trackingID string, update SecurityReportUpdate, now time.Time) (*SecurityReport, string, error) {
	var currentStatus string
	var updated *SecurityReport
	err := WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		current, err := scanSecurityReport(tx.QueryRowContext(ctx, securityReportSelect+` WHERE tracking_id = ?`, trackingID))
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
		severity := current.Severity
		if update.Severity != nil {
			if !IsSecurityReportSeverity(*update.Severity) {
				return fmt.Errorf("db: invalid security report severity %q", *update.Severity)
			}
			severity = *update.Severity
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
		if _, err := tx.ExecContext(ctx, `UPDATE security_reports SET status=?, severity=?, researcher_comment=?, comments=?, disclosure_date=?, disclosure_at=?, closed_at=?, updated_at=? WHERE tracking_id=?`, newStatus, severity, comments, comments, nullableDate(disclosure), unixOrZero(disclosure), unixOrZero(closed), unixOrZero(now), trackingID); err != nil {
			return HandleQueryError(err)
		}
		if newStatus == StatusDisclosed && current.CreditPref != "no" {
			var credit string
			if current.CreditPref == "anon" {
				var count int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_thanks WHERE credit LIKE 'Anonymous Researcher #%'`).Scan(&count); err != nil {
					return HandleQueryError(err)
				}
				credit = fmt.Sprintf("Anonymous Researcher #%d", count+1)
			} else {
				credit = current.CreditName
			}
			if credit != "" {
				if _, err := tx.ExecContext(ctx, `INSERT INTO security_thanks (tracking_id, credit, severity, disclosed_on) VALUES (?, ?, ?, ?) ON CONFLICT(tracking_id) DO NOTHING`, trackingID, credit, severity, now.UTC().Format("2006-01-02")); err != nil {
					return HandleQueryError(err)
				}
			}
		}
		updated, err = scanSecurityReport(tx.QueryRowContext(ctx,
			securityReportSelect+` WHERE tracking_id = ?`, trackingID))
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
	cutoffDate := viewedAt.UTC().Add(-interval).Format("2006-01-02")
	result, err := sqlDB.ExecContext(ctx,
		`UPDATE security_reports SET last_viewed_at = ?, token_last_used = date(?, 'unixepoch') WHERE tracking_id = ? AND (token_last_used IS NULL OR token_last_used <= ?)`,
		now, now, trackingID, cutoffDate)
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
		`UPDATE security_reports SET last_viewed_at = ?, token_last_used = date(?, 'unixepoch') WHERE tracking_id = ?`,
		unixOrZero(viewedAt), unixOrZero(viewedAt), trackingID)
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

func defaultCreditPref(value string) string {
	if value == "" {
		return "no"
	}
	return value
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableDate(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format("2006-01-02")
}
