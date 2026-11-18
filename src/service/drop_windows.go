//go:build windows

package service

// DropPrivileges is a no-op on Windows; services run as the configured
// Virtual Service Account.
func DropPrivileges(int) error { return nil }
