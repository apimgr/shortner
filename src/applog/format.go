package applog

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// timeRFC3339 formats t the way AI.md PART 11 "Log Format Details"
// requires for text/logfmt lines: RFC 3339 with a timezone offset.
func timeRFC3339(t time.Time) string {
	return t.Format(time.RFC3339)
}

// FormatText renders a server/error/debug log line, per AI.md PART 11
// "Text Log Format": "2024-10-10T13:55:36-04:00 [INFO] Server started on
// :8080".
func FormatText(t time.Time, level, msg string) string {
	return fmt.Sprintf("%s [%s] %s\n", timeRFC3339(t), strings.ToUpper(level), msg)
}

// FormatLogfmt renders an app.log line, per AI.md PART 11 "app.log
// (logfmt) — example line": space-separated key=value pairs, RFC 3339
// timestamp, values containing spaces or `=` quoted.
func FormatLogfmt(t time.Time, level, msg string, fields map[string]string) string {
	var b strings.Builder
	b.WriteString("time=")
	b.WriteString(timeRFC3339(t))
	b.WriteString(" level=")
	b.WriteString(strings.ToUpper(level))
	b.WriteString(" msg=")
	b.WriteString(logfmtQuote(msg))

	for _, k := range sortedKeys(fields) {
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(logfmtQuote(fields[k]))
	}
	b.WriteByte('\n')
	return b.String()
}

// logfmtQuote quotes v with double quotes if it contains a space, `=`, or
// a double quote; otherwise returns it unquoted.
func logfmtQuote(v string) string {
	if strings.ContainsAny(v, " =\"") {
		return strconv.Quote(v)
	}
	return v
}

// sortedKeys returns m's keys in a stable (sorted) order so logfmt/JSON
// output is deterministic and diff-friendly.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// AccessLogEntry captures one HTTP request for access.log, per AI.md
// PART 11 "Access Log Formats".
type AccessLogEntry struct {
	IP        string
	Time      time.Time
	Method    string
	Path      string
	Protocol  string
	Status    int
	Size      int64
	Referer   string
	UserAgent string
}

// FormatApache renders e in Apache Combined Log Format (access.log
// default), per AI.md PART 11 "Access Log Formats".
func FormatApache(e AccessLogEntry) string {
	referer := e.Referer
	if referer == "" {
		referer = "-"
	}
	ua := e.UserAgent
	if ua == "" {
		ua = "-"
	}
	proto := e.Protocol
	if proto == "" {
		proto = "HTTP/1.1"
	}
	return fmt.Sprintf("%s - - [%s] \"%s %s %s\" %d %d \"%s\" \"%s\"\n",
		e.IP, e.Time.Format("02/Jan/2006:15:04:05 -0700"),
		e.Method, e.Path, proto, e.Status, e.Size, referer, ua)
}

// FormatNginx renders e in Nginx Common Log Format, per AI.md PART 11
// "Access Log Formats".
func FormatNginx(e AccessLogEntry) string {
	proto := e.Protocol
	if proto == "" {
		proto = "HTTP/1.1"
	}
	return fmt.Sprintf("%s - - [%s] \"%s %s %s\" %d %d\n",
		e.IP, e.Time.Format("02/Jan/2006:15:04:05 -0700"),
		e.Method, e.Path, proto, e.Status, e.Size)
}

// FormatAccessJSON renders e as structured JSON, per AI.md PART 11
// "Access Log Formats" -> `json`.
func FormatAccessJSON(e AccessLogEntry) string {
	return fmt.Sprintf(
		`{"ip":%s,"time":%s,"method":%s,"path":%s,"status":%d,"size":%d,"ua":%s}`+"\n",
		jsonString(e.IP), jsonString(e.Time.UTC().Format(time.RFC3339)),
		jsonString(e.Method), jsonString(e.Path), e.Status, e.Size, jsonString(e.UserAgent))
}

// jsonString returns v as a double-quoted, escaped JSON string literal.
func jsonString(v string) string {
	return strconv.Quote(v)
}

// FormatSyslogRFC3164 renders an auth.log line, per AI.md PART 11
// "auth.log (syslog RFC 3164) — example line": "<MMM DD HH:MM:SS>
// <hostname> <program>[<pid>]:" followed by structured message.
func FormatSyslogRFC3164(hostname, program string, pid int, t time.Time, message string) string {
	return fmt.Sprintf("%s %s %s[%d]: %s\n", t.Format("Jan  2 15:04:05"), hostname, program, pid, message)
}

// FormatFail2ban renders a security.log line, per AI.md PART 11
// "Fail2ban Format": "2024-10-10T13:55:36-04:00 [security] Failed
// authentication attempt from 192.168.1.100".
func FormatFail2ban(t time.Time, message string) string {
	return fmt.Sprintf("%s [security] %s\n", timeRFC3339(t), message)
}

// FormatCEF renders a minimal ArcSight Common Event Format line for
// security.log, per AI.md PART 11 "Security Log Formats" -> `cef`.
// vendor/product/version identify this application as the CEF device.
func FormatCEF(vendor, product, version string, severity int, event, message string, extension map[string]string) string {
	var ext strings.Builder
	for i, k := range sortedKeys(extension) {
		if i > 0 {
			ext.WriteByte(' ')
		}
		ext.WriteString(k)
		ext.WriteByte('=')
		ext.WriteString(cefEscape(extension[k]))
	}
	return fmt.Sprintf("CEF:0|%s|%s|%s|%s|%s|%d|%s\n",
		vendor, product, version, event, message, severity, ext.String())
}

// cefEscape escapes CEF extension-field reserved characters (`=`, `\`,
// newline), per the CEF specification.
func cefEscape(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, "=", "\\=")
	v = strings.ReplaceAll(v, "\n", "\\n")
	return v
}
