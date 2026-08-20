// Small parsing helpers shared by the service managers.
package service

import (
	"errors"
	"strconv"
	"strings"
)

// errNoSysVTool is returned when neither update-rc.d nor chkconfig is
// available to toggle a SysVinit service's auto-start.
var errNoSysVTool = errors.New("service: neither update-rc.d nor chkconfig is available to enable/disable the service")

// containsWord reports whether text contains name as a whitespace
// delimited field, used to read `rc-update show` output without matching
// a longer service name that merely has this one as a prefix.
func containsWord(text, name string) bool {
	for _, field := range strings.Fields(text) {
		if field == name {
			return true
		}
	}
	return false
}

// hasPrefixWord reports whether the first whitespace-delimited field of
// text (with any trailing colon removed) equals word — the shape of
// `sv status` output, e.g. "run: shortner: (pid 123) 5s".
func hasPrefixWord(text, word string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	return strings.TrimSuffix(fields[0], ":") == word
}

// runitPID extracts the PID from `sv status` output of the form
// "run: {name}: (pid 1234) 12s".
func runitPID(out string) int {
	idx := strings.Index(out, "(pid ")
	if idx < 0 {
		return 0
	}
	rest := out[idx+len("(pid "):]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// isYes reports whether an rc.conf boolean value is enabled.
func isYes(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "YES", "TRUE", "ON", "1":
		return true
	}
	return false
}
