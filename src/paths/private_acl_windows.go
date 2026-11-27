//go:build windows

package paths

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// EnforcePrivateACL protects an existing private subtree when the server is
// using system paths. User-mode paths keep their profile-inherited ACLs.
func EnforcePrivateACL(root string) error {
	if root == "" || !IsAdministrator() {
		return nil
	}
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("paths: inspect private path %s: %w", root, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("paths: refusing symlink in private path %s", root)
	}

	serviceSID, err := serviceSID(internalName)
	if err != nil {
		return fmt.Errorf("paths: resolve service SID for private path %s: %w", root, err)
	}
	systemSID, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return fmt.Errorf("paths: resolve SYSTEM SID: %w", err)
	}
	adminsSID, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		return fmt.Errorf("paths: resolve Administrators SID: %w", err)
	}

	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("paths: inspect private path %s: %w", path, walkErr)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("paths: refusing symlink in private path %s", path)
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return fmt.Errorf("paths: refusing non-regular private path %s", path)
		}

		inheritance := uint32(0)
		if entry.IsDir() {
			inheritance = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
		}
		acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
			privateACE(serviceSID, windows.TRUSTEE_IS_USER, inheritance),
			privateACE(systemSID, windows.TRUSTEE_IS_USER, inheritance),
			privateACE(adminsSID, windows.TRUSTEE_IS_GROUP, inheritance),
		}, nil)
		if err != nil {
			return fmt.Errorf("paths: build protected ACL for %s: %w", path, err)
		}
		if err := windows.SetNamedSecurityInfo(
			path,
			windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
			nil,
			nil,
			acl,
			nil,
		); err != nil {
			return fmt.Errorf("paths: apply protected ACL to %s: %w", path, err)
		}
		return nil
	})
}

func privateACE(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE, inheritance uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.ACCESS_MASK(windows.GENERIC_ALL),
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

// serviceSID returns the per-service SID derived from the upper-case UTF-16
// service name, including before Windows has installed the service entry.
func serviceSID(name string) (*windows.SID, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("empty service name")
	}
	encoded := utf16.Encode([]rune(strings.ToUpper(name)))
	nameBytes := make([]byte, len(encoded)*2)
	for i, unit := range encoded {
		binary.LittleEndian.PutUint16(nameBytes[i*2:], unit)
	}
	digest := sha1.Sum(nameBytes)
	serviceSID := fmt.Sprintf("S-1-5-80-%d-%d-%d-%d-%d",
		binary.LittleEndian.Uint32(digest[0:4]),
		binary.LittleEndian.Uint32(digest[4:8]),
		binary.LittleEndian.Uint32(digest[8:12]),
		binary.LittleEndian.Uint32(digest[12:16]),
		binary.LittleEndian.Uint32(digest[16:20]),
	)
	return windows.StringToSid(serviceSID)
}
