//go:build windows

// Package privatefs creates and checks private CLI configuration and state.
package privatefs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FILE_ALL_ACCESS is not exported by x/sys/windows. Descendants inherit these
// grants rather than the process token's default DACL, including when elevated.
const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

func privateDescriptor(user *windows.SID, directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	return windows.SecurityDescriptorFromString("O:" + user.String() + "D:P" +
		"(A;" + inherit + ";FA;;;" + user.String() + ")" +
		"(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)")
}

// EnsureDir creates private storage at path and checks it and everything below
// it. Each foreign directory belongs to other software, such as the WebView2
// user data folder whose sandbox ACLs are its own: the directory itself must
// be private, but its contents are not checked.
func EnsureDir(path string, foreign ...string) error {
	path = filepath.Clean(path)
	user, err := currentUserSID()
	if err != nil {
		return err
	}
	descriptor, err := privateDescriptor(user, true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{
		Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor,
	}
	if err := windows.CreateDirectory(name, &attributes); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return &os.PathError{Op: "mkdir", Path: path, Err: err}
	}
	if err := checkPath(path, user, true, true); err != nil {
		return fmt.Errorf("state_dir must be owned by the current Windows user with a protected private ACL: %w", err)
	}
	// Windows normally grants bypass-traverse privilege: a private parent alone
	// cannot protect an existing child whose own DACL grants public access.
	return filepath.WalkDir(path, func(child string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if child != path {
			if err := checkPath(child, user, entry.IsDir(), false); err != nil {
				return fmt.Errorf("state_dir contains an unsafe entry %q: %w", child, err)
			}
		}
		for _, directory := range foreign {
			if entry.IsDir() && strings.EqualFold(child, filepath.Clean(directory)) {
				return fs.SkipDir
			}
		}
		return nil
	})
}

func CreateFile(path string) (*os.File, error) {
	user, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	descriptor, err := privateDescriptor(user, false)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	attributes := windows.SecurityAttributes{
		Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor,
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &attributes, windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	if err := checkHandle(handle, user, false, true); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func CheckFile(path string) error {
	user, err := currentUserSID()
	if err != nil {
		return err
	}
	return checkPath(path, user, false, false)
}

func checkPath(path string, user *windows.SID, directory, root bool) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return &os.PathError{Op: "check private storage", Path: path, Err: err}
	}
	defer windows.CloseHandle(handle)
	return checkHandle(handle, user, directory, root)
}

func checkHandle(handle windows.Handle, user *windows.SID, directory, root bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		(info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		return errors.New("private storage must be a regular file or directory, not a reparse point")
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	return checkDescriptor(descriptor, user, directory, root)
}

func checkDescriptor(descriptor *windows.SECURITY_DESCRIPTOR, user *windows.SID, directory, root bool) error {
	owner, _, err := descriptor.Owner()
	if err != nil {
		return err
	}
	// An elevated token may make Administrators the owner of newly created
	// descendants. SYSTEM and administrators are trusted, as root is on Unix.
	trusted := func(sid *windows.SID) bool {
		return sid != nil && (sid.Equals(user) || sid.IsWellKnown(windows.WinLocalSystemSid) ||
			sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
	}
	if owner == nil || root && !owner.Equals(user) || !trusted(owner) {
		return errors.New("private storage has an unexpected Windows owner")
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return err
	}
	if root && control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("private storage must not inherit permissions from its parent")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return errors.New("private storage must not have a null DACL")
	}
	var userAccess, inheritedAccess windows.ACCESS_MASK
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return err
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("private storage has an unsupported ACL entry")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Mask != 0 && !trusted(sid) {
			return errors.New("private storage grants access outside the current user, SYSTEM and administrators")
		}
		if sid.Equals(user) {
			if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0 {
				userAccess |= ace.Mask
			}
			const inherit = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
			if ace.Header.AceFlags&inherit == inherit && ace.Header.AceFlags&windows.NO_PROPAGATE_INHERIT_ACE == 0 {
				inheritedAccess |= ace.Mask
			}
		}
	}
	if userAccess&fileAllAccess != fileAllAccess || directory && inheritedAccess&fileAllAccess != fileAllAccess {
		return errors.New("private storage must grant the current user full control, inherited by all children")
	}
	return nil
}
