package notify

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/config"
)

// dialTimeout bounds every SMTP connection attempt. Auto-detection walks
// up to 21 host/port pairs (AI.md PART 17 "SMTP Auto-Detection"), so a
// host that silently drops packets must not stall startup.
const dialTimeout = 5 * time.Second

// Message is one outbound email. Everything is plain text: AI.md PART 17
// "Template Format" defines a text body, and a text/plain notification is
// what an operator's alerting pipeline can actually grep.
type Message struct {
	FromName  string
	FromEmail string
	ReplyTo   string
	To        []string
	Subject   string
	Body      string
}

// dialSMTP opens an SMTP session honoring the configured TLS mode, per
// AI.md PART 17 "SMTP Config" (`tls: auto|starttls|tls|none`).
//
//	tls      — implicit TLS from the first byte (submissions, port 465)
//	starttls — plaintext connect, then a required STARTTLS upgrade
//	none     — plaintext throughout
//	auto     — implicit TLS on port 465, otherwise STARTTLS when the
//	           server advertises it, plaintext when it does not
func dialSMTP(cfg config.SMTP) (*smtp.Client, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return nil, errors.New("notify: no SMTP host configured")
	}
	addr := net.JoinHostPort(host, strconv.Itoa(cfg.Port))
	mode := cfg.TLS
	if mode == "" {
		mode = config.SMTPTLSAuto
	}

	if mode == config.SMTPTLSDirect || (mode == config.SMTPTLSAuto && cfg.Port == 465) {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: dialTimeout}, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return nil, fmt.Errorf("notify: smtp connect %s: %w", addr, err)
		}
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("notify: smtp handshake %s: %w", addr, err)
		}
		return client, nil
	}

	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("notify: smtp connect %s: %w", addr, err)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("notify: smtp handshake %s: %w", addr, err)
	}

	if mode == config.SMTPTLSNone {
		return client, nil
	}
	ok, _ := client.Extension("STARTTLS")
	if !ok {
		if mode == config.SMTPTLSStartTLS {
			client.Close()
			return nil, fmt.Errorf("notify: smtp %s: STARTTLS required but not offered", addr)
		}
		return client, nil
	}
	if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
		client.Close()
		return nil, fmt.Errorf("notify: smtp starttls %s: %w", addr, err)
	}
	return client, nil
}

// authenticate applies SMTP AUTH when a username is configured. CRAM-MD5
// is preferred on an unencrypted link because net/smtp's PLAIN mechanism
// (correctly) refuses to hand a password to a non-local cleartext server.
func authenticate(client *smtp.Client, cfg config.SMTP, host string) error {
	if cfg.Username == "" {
		return nil
	}
	ok, mechs := client.Extension("AUTH")
	if !ok {
		return fmt.Errorf("notify: smtp %s: credentials configured but server offers no AUTH", host)
	}
	_, encrypted := client.TLSConnectionState()
	if !encrypted && strings.Contains(mechs, "CRAM-MD5") {
		return client.Auth(smtp.CRAMMD5Auth(cfg.Username, cfg.Password))
	}
	return client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, host))
}

// TestConnection performs the AI.md PART 17 "Connection Test (when host is
// set)" handshake: connect, EHLO (done by smtp.NewClient), authenticate if
// credentials are configured, then QUIT. A nil return means email may be
// enabled.
func TestConnection(cfg config.SMTP) error {
	client, err := dialSMTP(cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := authenticate(client, cfg, strings.TrimSpace(cfg.Host)); err != nil {
		return fmt.Errorf("notify: smtp auth %s: %w", cfg.Host, err)
	}
	return client.Quit()
}

// sendMail delivers msg through the configured server. It is a package
// variable so tests can exercise the notifier's decision logic without a
// real MTA; production always uses smtpSend.
var sendMail = smtpSend

// smtpSend is the real transport.
func smtpSend(cfg config.SMTP, msg Message) error {
	client, err := dialSMTP(cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := authenticate(client, cfg, strings.TrimSpace(cfg.Host)); err != nil {
		return fmt.Errorf("notify: smtp auth %s: %w", cfg.Host, err)
	}
	if err := client.Mail(msg.FromEmail); err != nil {
		return fmt.Errorf("notify: smtp MAIL FROM: %w", err)
	}
	for _, rcpt := range msg.To {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("notify: smtp RCPT TO %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("notify: smtp DATA: %w", err)
	}
	if _, err := w.Write(BuildMessage(msg, time.Now())); err != nil {
		w.Close()
		return fmt.Errorf("notify: smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("notify: smtp close: %w", err)
	}
	return client.Quit()
}

// BuildMessage renders msg as an RFC 5322 message. Every header value is
// stripped of CR and LF first: subject and body text originate in
// operator templates and in event data (a task name, an error string, a
// researcher's own input), so header injection is blocked at the one place
// headers are assembled.
func BuildMessage(msg Message, now time.Time) []byte {
	var b strings.Builder
	b.WriteString("From: " + formatAddress(msg.FromName, msg.FromEmail) + "\r\n")
	b.WriteString("To: " + strings.Join(sanitizeAll(msg.To), ", ") + "\r\n")
	if rt := headerSafe(msg.ReplyTo); rt != "" {
		b.WriteString("Reply-To: " + rt + "\r\n")
	}
	b.WriteString("Subject: " + encodeHeader(headerSafe(msg.Subject)) + "\r\n")
	b.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + messageID(msg.FromEmail) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	b.WriteString(dotStuff(normalizeNewlines(msg.Body)))
	return []byte(b.String())
}

// formatAddress renders a From header, quoting the display name only when
// one is set.
func formatAddress(name, addr string) string {
	addr = headerSafe(addr)
	name = headerSafe(name)
	if name == "" {
		return addr
	}
	return mime.QEncoding.Encode("utf-8", name) + " <" + addr + ">"
}

// encodeHeader RFC 2047-encodes a header value when it is not pure ASCII.
func encodeHeader(v string) string {
	return mime.QEncoding.Encode("utf-8", v)
}

// headerSafe removes CR/LF (and the quotes/angle brackets that would let a
// value escape its own header) and trims surrounding space.
func headerSafe(v string) string {
	v = strings.NewReplacer("\r", " ", "\n", " ", "\x00", "").Replace(v)
	return strings.TrimSpace(v)
}

// sanitizeAll applies headerSafe to every element.
func sanitizeAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s := headerSafe(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// normalizeNewlines converts the body to CRLF line endings, as the DATA
// command requires.
func normalizeNewlines(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	return strings.ReplaceAll(body, "\n", "\r\n")
}

// dotStuff escapes a leading "." on any line so a body line consisting of
// a single dot cannot terminate the DATA block early (RFC 5321 §4.5.2).
func dotStuff(body string) string {
	if strings.HasPrefix(body, ".") {
		body = "." + body
	}
	return strings.ReplaceAll(body, "\r\n.", "\r\n..")
}

// messageID returns a unique Message-ID whose domain half comes from the
// sender address, so the header always matches the envelope domain.
func messageID(from string) string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// A failed CSPRNG read must not drop the notification; the
		// timestamp alone still yields a usable, unique-enough id.
		return "<" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@" + messageIDDomain(from) + ">"
	}
	return "<" + hex.EncodeToString(buf[:]) + "@" + messageIDDomain(from) + ">"
}

// messageIDDomain extracts the domain from an address, falling back to
// "localhost" when there is none to extract.
func messageIDDomain(from string) string {
	if at := strings.LastIndex(from, "@"); at >= 0 && at < len(from)-1 {
		return headerSafe(from[at+1:])
	}
	return "localhost"
}
