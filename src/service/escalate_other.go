//go:build !windows

// The Windows Administrators-group check has no meaning off Windows; the
// Unix side answers group membership through user.LookupGroupId instead
// (see inAdminLikeGroup in escalate.go).
package service

// inWindowsAdminGroup is always false on non-Windows platforms.
func inWindowsAdminGroup() bool { return false }
