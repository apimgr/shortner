package service

import (
	"strings"
	"testing"
)

// templateParams is a fixed parameter set so the rendered templates are
// deterministic.
func templateParams() Params {
	return Params{
		InternalName: "shortner",
		InternalOrg:  "apimgr",
		ProjectName:  "shortner",
		ProjectOrg:   "apimgr",
		AppName:      "Shortner",
		BinaryPath:   "/usr/local/bin/shortner",
		ConfigDir:    "/etc/apimgr/shortner",
		DataDir:      "/var/lib/apimgr/shortner",
		CacheDir:     "/var/cache/apimgr/shortner",
		LogDir:       "/var/log/apimgr/shortner",
		BackupDir:    "/var/lib/apimgr/shortner/backups",
		PIDFile:      "/run/apimgr/shortner/server.pid",
	}
}

// mustContain asserts every fragment is present in the rendered output.
func mustContain(t *testing.T, name, out string, fragments []string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(out, fragment) {
			t.Errorf("%s is missing %q\ngot:\n%s", name, fragment, out)
		}
	}
}

func TestPlistName(t *testing.T) {
	// PART 24 "launchd (macOS)": io.github.{project_org}.{internal_name}.
	if got, want := templateParams().PlistName(), "io.github.apimgr.shortner"; got != want {
		t.Errorf("PlistName() = %q, want %q", got, want)
	}
}

func TestSystemdUnit(t *testing.T) {
	out := systemdUnit(templateParams())
	mustContain(t, "systemd unit", out, []string{
		"[Unit]",
		"Description=shortner service",
		"After=network-online.target",
		"Wants=network-online.target",
		"[Service]",
		"Type=simple",
		"ExecStart=/usr/local/bin/shortner",
		"Restart=on-failure",
		"RestartSec=5",
		"StandardOutput=journal",
		"StandardError=journal",
		"ProtectSystem=strict",
		"ProtectHome=yes",
		"PrivateTmp=yes",
		"ReadWritePaths=/etc/apimgr/shortner",
		"ReadWritePaths=/var/lib/apimgr/shortner",
		"ReadWritePaths=/var/cache/apimgr/shortner",
		"ReadWritePaths=/var/log/apimgr/shortner",
		"[Install]",
		"WantedBy=multi-user.target",
	})
}

// TestSystemdUnitUserLevel covers the PART 23 user-service fallback: a
// --user unit belongs to default.target, not multi-user.target.
func TestSystemdUnitUserLevel(t *testing.T) {
	p := templateParams()
	p.UserLevel = true
	out := systemdUnit(p)
	if !strings.Contains(out, "WantedBy=default.target") {
		t.Errorf("user unit missing WantedBy=default.target\ngot:\n%s", out)
	}
	if strings.Contains(out, "multi-user.target") {
		t.Errorf("user unit wrongly targets multi-user.target\ngot:\n%s", out)
	}
}

func TestOpenRCScript(t *testing.T) {
	out := openrcScript(templateParams())
	mustContain(t, "OpenRC script", out, []string{
		"#!/sbin/openrc-run",
		`name="shortner"`,
		`description="Shortner"`,
		`command="/usr/local/bin/shortner"`,
		`command_user="shortner:shortner"`,
		`pidfile="/run/apimgr/shortner/server.pid"`,
		"command_background=true",
		`output_log="/var/log/apimgr/shortner/server.log"`,
		`error_log="/var/log/apimgr/shortner/error.log"`,
		"depend() {",
		"need net",
		"after firewall",
		"use dns logger",
		"start_pre() {",
		"checkpath -d -m 0755 -o shortner:shortner /run/apimgr/shortner",
	})
}

func TestSysVinitScript(t *testing.T) {
	out := sysvinitScript(templateParams())
	mustContain(t, "SysVinit script", out, []string{
		"#!/bin/sh",
		"### BEGIN INIT INFO",
		"# Provides:          shortner",
		"# Default-Start:     2 3 4 5",
		"# Default-Stop:      0 1 6",
		"### END INIT INFO",
		"NAME=shortner",
		"DAEMON=/usr/local/bin/shortner",
		"DAEMON_USER=shortner",
		"PIDFILE=/run/apimgr/shortner/server.pid",
		"LOGFILE=/var/log/apimgr/shortner/server.log",
		"start-stop-daemon --start",
		"start-stop-daemon --stop",
		"Usage: $0 {start|stop|restart|status}",
	})
}

func TestRunitScripts(t *testing.T) {
	p := templateParams()
	mustContain(t, "runit run", runitRunScript(p), []string{
		"#!/bin/sh",
		"exec /usr/local/bin/shortner 2>&1",
	})
	mustContain(t, "runit log/run", runitLogRunScript(p), []string{
		"#!/bin/sh",
		"exec svlogd -tt /var/log/apimgr/shortner",
	})
}

func TestRCdScript(t *testing.T) {
	out := rcdScript(templateParams())
	mustContain(t, "rc.d script", out, []string{
		"#!/bin/sh",
		"# PROVIDE: shortner",
		"# REQUIRE: NETWORKING",
		"# KEYWORD: shutdown",
		". /etc/rc.subr",
		`name="shortner"`,
		`rcvar="shortner_enable"`,
		`command="/usr/local/bin/shortner"`,
		"load_rc_config $name",
		`run_rc_command "$1"`,
	})
}

func TestLaunchdPlist(t *testing.T) {
	out := launchdPlist(templateParams())
	mustContain(t, "launchd plist", out, []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<plist version="1.0">`,
		"<key>Label</key>",
		"<string>io.github.apimgr.shortner</string>",
		"<key>ProgramArguments</key>",
		"<string>/usr/local/bin/shortner</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<string>/var/log/apimgr/shortner/stdout.log</string>",
		"<string>/var/log/apimgr/shortner/stderr.log</string>",
		"</plist>",
	})
	// PART 23: the daemon starts as root and drops privileges itself, so
	// the plist must not pin a UserName.
	if strings.Contains(out, "<key>UserName</key>") {
		t.Error("plist wrongly sets UserName")
	}
}

// TestTemplatesEndWithNewline keeps every generated file conforming to
// the single-trailing-newline rule.
func TestTemplatesEndWithNewline(t *testing.T) {
	p := templateParams()
	rendered := map[string]string{
		"systemd":  systemdUnit(p),
		"openrc":   openrcScript(p),
		"sysvinit": sysvinitScript(p),
		"runit":    runitRunScript(p),
		"runitlog": runitLogRunScript(p),
		"rcd":      rcdScript(p),
		"launchd":  launchdPlist(p),
	}
	for name, out := range rendered {
		if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
			t.Errorf("%s template does not end with exactly one newline", name)
		}
	}
}
