// Service definition templates, transcribed from AI.md PART 24 "Service
// Templates" and PART 23 "launchd plist". The only substitutions are the
// project's frozen identifiers (IDEA.md "Project variables") and the
// runtime directories resolved by src/paths — the structure, ordering,
// and comments of each template match the spec.
package service

import (
	"path/filepath"
	"strings"
)

// systemdUnit renders the systemd unit for /etc/systemd/system/
// {internal_name}.service (PART 24 "systemd (Linux)").
//
// ExecStart uses the resolved binary path rather than a hardcoded
// /usr/local/bin/{project_name} so an install from a staging or user
// prefix produces a unit that actually starts; on a system install the
// two are the same value (src/paths Binary).
func systemdUnit(p Params) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=" + p.ProjectName + " service\n")
	b.WriteString("Documentation=https://" + p.ProjectOrg + ".github.io/" + p.ProjectName + "\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n")
	b.WriteString("\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("ExecStart=" + p.BinaryPath + "\n")
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=5\n")
	b.WriteString("StandardOutput=journal\n")
	b.WriteString("StandardError=journal\n")
	b.WriteString("\n")
	b.WriteString("# Security hardening (binary drops privileges after port binding)\n")
	b.WriteString("ProtectSystem=strict\n")
	b.WriteString("ProtectHome=yes\n")
	b.WriteString("PrivateTmp=yes\n")
	for _, dir := range []string{p.ConfigDir, p.DataDir, p.CacheDir, p.LogDir} {
		if dir != "" {
			b.WriteString("ReadWritePaths=" + dir + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString("[Install]\n")
	if p.UserLevel {
		// A --user unit is owned by the login session, not multi-user.target.
		b.WriteString("WantedBy=default.target\n")
	} else {
		b.WriteString("WantedBy=multi-user.target\n")
	}
	return b.String()
}

// openrcScript renders /etc/init.d/{internal_name} for OpenRC
// (PART 24 "OpenRC (Alpine, Gentoo, Devuan)").
func openrcScript(p Params) string {
	runDir := filepath.Dir(p.PIDFile)
	var b strings.Builder
	b.WriteString("#!/sbin/openrc-run\n")
	b.WriteString("# Service identity comes from " + p.InternalName + " so config_dir/data_dir paths stay\n")
	b.WriteString("# stable across binary renames.\n")
	b.WriteString("\n")
	b.WriteString("name=\"" + p.InternalName + "\"\n")
	b.WriteString("description=\"" + p.AppName + "\"\n")
	b.WriteString("# actual binary (may differ from " + p.InternalName + " after rename)\n")
	b.WriteString("command=\"" + p.BinaryPath + "\"\n")
	b.WriteString("command_args=\"\"\n")
	b.WriteString("command_user=\"" + p.InternalName + ":" + p.InternalName + "\"\n")
	b.WriteString("pidfile=\"" + p.PIDFile + "\"\n")
	b.WriteString("command_background=true\n")
	b.WriteString("output_log=\"" + filepath.Join(p.LogDir, "server.log") + "\"\n")
	b.WriteString("error_log=\"" + filepath.Join(p.LogDir, "error.log") + "\"\n")
	b.WriteString("\n")
	b.WriteString("depend() {\n")
	b.WriteString("    need net\n")
	b.WriteString("    after firewall\n")
	b.WriteString("    use dns logger\n")
	b.WriteString("}\n")
	b.WriteString("\n")
	b.WriteString("start_pre() {\n")
	b.WriteString("    checkpath -d -m 0755 -o " + p.InternalName + ":" + p.InternalName + " " + runDir + "\n")
	b.WriteString("    checkpath -d -m 0755 -o " + p.InternalName + ":" + p.InternalName + " " + p.LogDir + "\n")
	b.WriteString("}\n")
	return b.String()
}

// sysvinitScript renders /etc/init.d/{internal_name} for SysVinit
// (PART 24 "SysVinit (legacy Linux, init.d)"). Same path as the OpenRC
// script — only one of the two is ever installed, decided by detect().
func sysvinitScript(p Params) string {
	logFile := filepath.Join(p.LogDir, "server.log")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("### BEGIN INIT INFO\n")
	b.WriteString("# Provides:          " + p.InternalName + "\n")
	b.WriteString("# Required-Start:    $network $remote_fs $syslog\n")
	b.WriteString("# Required-Stop:     $network $remote_fs $syslog\n")
	b.WriteString("# Default-Start:     2 3 4 5\n")
	b.WriteString("# Default-Stop:      0 1 6\n")
	b.WriteString("# Short-Description: " + p.AppName + "\n")
	b.WriteString("# Description:       " + p.AppName + " daemon\n")
	b.WriteString("### END INIT INFO\n")
	b.WriteString("\n")
	b.WriteString("NAME=" + p.InternalName + "\n")
	b.WriteString("DAEMON=" + p.BinaryPath + "\n")
	b.WriteString("DAEMON_USER=" + p.InternalName + "\n")
	b.WriteString("PIDFILE=" + p.PIDFile + "\n")
	b.WriteString("LOGFILE=" + logFile + "\n")
	b.WriteString("\n")
	b.WriteString(`case "$1" in
    start)
        echo "Starting $NAME..."
        mkdir -p $(dirname $PIDFILE) $(dirname $LOGFILE)
        chown -R $DAEMON_USER:$DAEMON_USER $(dirname $PIDFILE) $(dirname $LOGFILE)
        start-stop-daemon --start --quiet --background --make-pidfile \
            --pidfile $PIDFILE --chuid $DAEMON_USER --exec $DAEMON \
            --no-close >> $LOGFILE 2>&1
        ;;
    stop)
        echo "Stopping $NAME..."
        start-stop-daemon --stop --quiet --pidfile $PIDFILE --retry 30
        rm -f $PIDFILE
        ;;
    restart)
        $0 stop
        sleep 1
        $0 start
        ;;
    status)
        if [ -f $PIDFILE ] && kill -0 $(cat $PIDFILE) 2>/dev/null; then
            echo "$NAME is running (pid $(cat $PIDFILE))"
            exit 0
        else
            echo "$NAME is stopped"
            exit 3
        fi
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac
exit 0
`)
	return b.String()
}

// runitRunScript renders /etc/sv/{internal_name}/run
// (PART 24 "runit (Linux)").
func runitRunScript(p Params) string {
	return "#!/bin/sh\nexec " + p.BinaryPath + " 2>&1\n"
}

// runitLogRunScript renders /etc/sv/{internal_name}/log/run
// (PART 24 "runit (Linux)").
func runitLogRunScript(p Params) string {
	return "#!/bin/sh\nexec svlogd -tt " + p.LogDir + "\n"
}

// rcdScript renders /usr/local/etc/rc.d/{internal_name}
// (PART 24 "rc.d (FreeBSD)").
func rcdScript(p Params) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("\n")
	b.WriteString("# PROVIDE: " + p.InternalName + "\n")
	b.WriteString("# REQUIRE: NETWORKING\n")
	b.WriteString("# KEYWORD: shutdown\n")
	b.WriteString("\n")
	b.WriteString(". /etc/rc.subr\n")
	b.WriteString("\n")
	b.WriteString("name=\"" + p.InternalName + "\"\n")
	b.WriteString("rcvar=\"" + p.InternalName + "_enable\"\n")
	b.WriteString("command=\"" + p.BinaryPath + "\"\n")
	b.WriteString("\n")
	b.WriteString("load_rc_config $name\n")
	b.WriteString("run_rc_command \"$1\"\n")
	return b.String()
}

// launchdPlist renders /Library/LaunchDaemons/{plist_name}.plist
// (PART 24 "launchd (macOS)"). No UserName/GroupName key: PART 23 has the
// daemon start as root and the binary drop privileges after port binding.
func launchdPlist(p Params) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n")
	b.WriteString("<dict>\n")
	b.WriteString("    <key>Label</key>\n")
	b.WriteString("    <string>" + p.PlistName() + "</string>\n")
	b.WriteString("    <key>ProgramArguments</key>\n")
	b.WriteString("    <array>\n")
	b.WriteString("        <string>" + p.BinaryPath + "</string>\n")
	b.WriteString("    </array>\n")
	b.WriteString("    <key>RunAtLoad</key>\n")
	b.WriteString("    <true/>\n")
	b.WriteString("    <key>KeepAlive</key>\n")
	b.WriteString("    <true/>\n")
	b.WriteString("    <key>StandardOutPath</key>\n")
	b.WriteString("    <string>" + filepath.Join(p.LogDir, "stdout.log") + "</string>\n")
	b.WriteString("    <key>StandardErrorPath</key>\n")
	b.WriteString("    <string>" + filepath.Join(p.LogDir, "stderr.log") + "</string>\n")
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}
