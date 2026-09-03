//go:build windows

// Windows Administrators-group membership check. See AI.md PART 5
// "Binary Implementation" item 1 (`isInAdminGroup`): a user who is in the
// Administrators group can elevate through UAC even when the current
// token is not elevated.
package service

import "golang.org/x/sys/windows"

// inWindowsAdminGroup reports whether the current token carries the
// built-in Administrators group, elevated or not.
func inWindowsAdminGroup() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	token := windows.Token(0)
	groups, err := token.GetTokenGroups()
	if err != nil {
		return false
	}
	for _, g := range groups.AllGroups() {
		if windows.EqualSid(g.Sid, sid) {
			return true
		}
	}
	return false
}
