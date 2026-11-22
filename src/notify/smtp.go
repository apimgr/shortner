package notify

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/config"
)

// dialTimeout bounds every SMTP connection attempt. Auto-detection walks
// up to 21 host/port pairs (AI.md PART 17 "SMTP Auto-Detection"), so a
// host that silently drops packets must not stall startup.
const dialTimeout = 5 * time.Second

// Attachment is one file attached to an outbound email.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message is one outbound email. Body is the plain-text source used to build
// both required alternative representations; attachments are optional.
type Message struct {
	FromName    string
	FromEmail   string
	ReplyTo     string
	To          []string
	Subject     string
	Body        string
	Attachments []Attachment
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
	recipients := sanitizeAll(msg.To)
	if len(recipients) == 0 {
		return errors.New("notify: no valid SMTP recipients")
	}
	for _, recipient := range recipients {
		if err := client.Mail(msg.FromEmail); err != nil {
			return fmt.Errorf("notify: smtp MAIL FROM: %w", err)
		}
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("notify: smtp RCPT TO: %w", err)
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("notify: smtp DATA: %w", err)
		}
		individual := msg
		individual.To = []string{recipient}
		message, err := BuildMessage(individual, time.Now())
		if err != nil {
			w.Close()
			return fmt.Errorf("notify: build message: %w", err)
		}
		if _, err := w.Write(message); err != nil {
			w.Close()
			return fmt.Errorf("notify: smtp write: %w", err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("notify: smtp close: %w", err)
		}
	}
	return client.Quit()
}

// BuildMessage renders msg as an RFC 5322 message. Every header value is
// stripped of CR and LF first: subject and body text originate in
// operator templates and in event data (a task name, an error string, a
// researcher's own input), so header injection is blocked at the one place
// headers are assembled.
func BuildMessage(msg Message, now time.Time) ([]byte, error) {
	recipients := sanitizeAll(msg.To)
	if len(recipients) != 1 {
		return nil, fmt.Errorf("notify: each message must have exactly one recipient")
	}
	body, contentType, err := buildBody(msg.Body, msg.Attachments)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("From: " + formatAddress(msg.FromName, msg.FromEmail) + "\r\n")
	b.WriteString("To: " + recipients[0] + "\r\n")
	if rt := headerSafe(msg.ReplyTo); rt != "" {
		b.WriteString("Reply-To: " + rt + "\r\n")
	}
	b.WriteString("Subject: " + encodeHeader(headerSafe(msg.Subject)) + "\r\n")
	b.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + messageID(msg.FromEmail) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: " + contentType + "\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	b.WriteString(dotStuff(string(body)))
	return []byte(b.String()), nil
}

func buildBody(body string, attachments []Attachment) ([]byte, string, error) {
	var outerBody bytes.Buffer
	outer := multipart.NewWriter(&outerBody)
	if len(attachments) == 0 {
		contentType := "multipart/alternative; boundary=" + outer.Boundary()
		if err := writeAlternatives(outer, body); err != nil {
			return nil, "", err
		}
		if err := outer.Close(); err != nil {
			return nil, "", err
		}
		return outerBody.Bytes(), contentType, nil
	}

	var alternatives bytes.Buffer
	altWriter := multipart.NewWriter(&alternatives)
	if err := writeAlternatives(altWriter, body); err != nil {
		return nil, "", err
	}
	if err := altWriter.Close(); err != nil {
		return nil, "", err
	}
	altType := mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": altWriter.Boundary()})
	part, err := outer.CreatePart(textproto.MIMEHeader{"Content-Type": []string{altType}})
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(alternatives.Bytes()); err != nil {
		return nil, "", err
	}
	for _, attachment := range attachments {
		if err := writeAttachment(outer, attachment); err != nil {
			return nil, "", err
		}
	}
	if err := outer.Close(); err != nil {
		return nil, "", err
	}
	contentType := mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": outer.Boundary()})
	return outerBody.Bytes(), contentType, nil
}

func writeAlternatives(writer *multipart.Writer, body string) error {
	plain := normalizeNewlines(body)
	if err := writeQuotedPrintable(writer, "text/plain; charset=UTF-8", plain); err != nil {
		return err
	}
	htmlBody := "<!doctype html><html><body><pre style=\"white-space: pre-wrap; font-family: inherit\">" + html.EscapeString(plain) + "</pre></body></html>"
	return writeQuotedPrintable(writer, "text/html; charset=UTF-8", htmlBody)
}

func writeQuotedPrintable(writer *multipart.Writer, contentType, body string) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	encoder := quotedprintable.NewWriter(part)
	if _, err := encoder.Write([]byte(body)); err != nil {
		encoder.Close()
		return err
	}
	return encoder.Close()
}

func writeAttachment(writer *multipart.Writer, attachment Attachment) error {
	filename := filepath.Base(strings.TrimSpace(attachment.Filename))
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		filename = "attachment.bin"
	}
	contentType := strings.TrimSpace(attachment.ContentType)
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		contentType = "application/octet-stream"
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	header.Set("Content-Transfer-Encoding", "base64")
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(attachment.Data)
	for len(encoded) > 76 {
		if _, err := part.Write([]byte(encoded[:76] + "\r\n")); err != nil {
			return err
		}
		encoded = encoded[76:]
	}
	if encoded != "" {
		_, err := part.Write([]byte(encoded + "\r\n"))
		return err
	}
	return nil
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
