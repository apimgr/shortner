package notify

import (
	"strings"
	"testing"
	"time"
)

func TestBuildMessage(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		msg         Message
		wantHas     []string
		wantMissing []string
	}{
		{
			name: "plain message carries the required headers",
			msg: Message{
				FromName:  "shortner",
				FromEmail: "noreply@example.com",
				To:        []string{"ops@example.com"},
				Subject:   "Backup complete",
				Body:      "all good\n",
			},
			wantHas: []string{
				"From: shortner <noreply@example.com>\r\n",
				"To: ops@example.com\r\n",
				"Subject: Backup complete\r\n",
				"MIME-Version: 1.0\r\n",
				"Content-Type: multipart/alternative; boundary=",
				"Content-Type: text/plain; charset=UTF-8\r\n",
				"Content-Type: text/html; charset=UTF-8\r\n",
				"Auto-Submitted: auto-generated\r\n",
				"@example.com>",
				"\r\n\r\nall good\r\n",
			},
			wantMissing: []string{"Reply-To:"},
		},
		{
			name: "reply-to is emitted only when set",
			msg: Message{
				FromEmail: "noreply@example.com",
				To:        []string{"ops@example.com"},
				ReplyTo:   "team@example.com",
				Subject:   "s",
				Body:      "b",
			},
			wantHas: []string{"Reply-To: team@example.com\r\n"},
		},
		{
			name: "CRLF injection in the subject is neutralized",
			msg: Message{
				FromEmail: "noreply@example.com",
				To:        []string{"ops@example.com"},
				Subject:   "hi\r\nBcc: attacker@evil.test",
				Body:      "b",
			},
			wantMissing: []string{"\r\nBcc: attacker@evil.test"},
			wantHas:     []string{"Subject: hi  Bcc: attacker@evil.test\r\n"},
		},
		{
			name: "one recipient is written to the To header",
			msg: Message{
				FromEmail: "noreply@example.com",
				To:        []string{"a@example.com"},
				Subject:   "s",
				Body:      "b",
			},
			wantHas: []string{"To: a@example.com\r\n"},
		},
		{
			name: "a lone dot line is stuffed",
			msg: Message{
				FromEmail: "noreply@example.com",
				To:        []string{"a@example.com"},
				Subject:   "s",
				Body:      "line\n.\nmore\n",
			},
			wantHas: []string{"\r\n..\r\n"},
		},
		{
			name: "a body starting with a dot is stuffed",
			msg: Message{
				FromEmail: "noreply@example.com",
				To:        []string{"a@example.com"},
				Subject:   "s",
				Body:      ".hidden\n",
			},
			wantHas: []string{"\r\n\r\n..hidden\r\n"},
		},
		{
			name: "non-ascii subject is RFC 2047 encoded",
			msg: Message{
				FromEmail: "noreply@example.com",
				To:        []string{"a@example.com"},
				Subject:   "café",
				Body:      "b",
			},
			wantHas:     []string{"=?utf-8?q?"},
			wantMissing: []string{"Subject: café"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			message, err := BuildMessage(tc.msg, now)
			if err != nil {
				t.Fatalf("BuildMessage() error = %v", err)
			}
			got := string(message)
			for _, want := range tc.wantHas {
				if !strings.Contains(got, want) {
					t.Errorf("message missing %q:\n%s", want, got)
				}
			}
			for _, bad := range tc.wantMissing {
				if strings.Contains(got, bad) {
					t.Errorf("message unexpectedly contains %q:\n%s", bad, got)
				}
			}
		})
	}
}

func TestBuildMessageRejectsMultipleRecipients(t *testing.T) {
	_, err := BuildMessage(Message{
		FromEmail: "noreply@example.com",
		To:        []string{"a@example.com", "b@example.com"},
		Subject:   "s",
		Body:      "b",
	}, time.Now())
	if err == nil {
		t.Fatal("BuildMessage() error = nil, want one recipient per message")
	}
}

func TestBuildMessageWithAttachment(t *testing.T) {
	message, err := BuildMessage(Message{
		FromEmail: "noreply@example.com",
		To:        []string{"security@example.com"},
		Subject:   "Security report",
		Body:      "The encrypted report is attached.",
		Attachments: []Attachment{{
			Filename: "security-report-sec_0123456789abcdef.enc", ContentType: "application/octet-stream", Data: []byte("sealed-ciphertext"),
		}},
	}, time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}
	got := string(message)
	for _, want := range []string{
		"Content-Type: multipart/mixed; boundary=",
		"Content-Type: multipart/alternative; boundary=",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Type: text/html; charset=UTF-8",
		"Content-Disposition: attachment; filename=security-report-sec_0123456789abcdef.enc",
		"Content-Transfer-Encoding: base64",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sealed-ciphertext") {
		t.Errorf("attachment contains unencoded ciphertext: %s", got)
	}
}

func TestHeaderSafe(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"  spaced  ", "spaced"},
		{"a\r\nb", "a  b"},
		{"a\nb", "a b"},
		{"nul\x00byte", "nulbyte"},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := headerSafe(tc.in); got != tc.want {
				t.Errorf("headerSafe(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeNewlines(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"lf becomes crlf", "a\nb", "a\r\nb"},
		{"crlf stays crlf", "a\r\nb", "a\r\nb"},
		{"mixed normalizes once", "a\r\nb\nc", "a\r\nb\r\nc"},
		{"no newline unchanged", "abc", "abc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeNewlines(tc.in); got != tc.want {
				t.Errorf("normalizeNewlines(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMessageIDDomain(t *testing.T) {
	tests := []struct{ in, want string }{
		{"noreply@example.com", "example.com"},
		{"noreply@", "localhost"},
		{"noaddress", "localhost"},
		{"", "localhost"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := messageIDDomain(tc.in); got != tc.want {
				t.Errorf("messageIDDomain(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatAddress(t *testing.T) {
	tests := []struct{ name, dispName, addr, want string }{
		{"no display name", "", "a@example.com", "a@example.com"},
		{"with display name", "shortner", "a@example.com", "shortner <a@example.com>"},
		{"injection in display name", "a\r\nb", "a@example.com", "a  b <a@example.com>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAddress(tc.dispName, tc.addr); got != tc.want {
				t.Errorf("formatAddress(%q, %q) = %q, want %q", tc.dispName, tc.addr, got, tc.want)
			}
		})
	}
}
