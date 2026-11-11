//go:build windows

package privatefs

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func checkStableDirectory(path string, _ os.FileInfo) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return errors.New("must be a directory, not a reparse point")
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	user, err := currentUserSID()
	if err != nil {
		return err
	}
	return checkStableDescriptor(descriptor, user)
}

func checkStableDescriptor(descriptor *windows.SECURITY_DESCRIPTOR, user *windows.SID) error {
	trusted := func(sid *windows.SID) bool {
		return sid != nil && (sid.Equals(user) || sid.IsWellKnown(windows.WinLocalSystemSid) ||
			sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) ||
			sid.String() == "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464") // TrustedInstaller
	}
	owner, _, err := descriptor.Owner()
	if err != nil || !trusted(owner) {
		return errors.New("storage ancestor has an unexpected owner")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("storage ancestor needs a Windows access policy")
	}
	// Read/traverse and adding subdirectories are harmless: new children are
	// exclusively created and ownership checked. Deleting/replacing a child or
	// changing an ancestor itself must be restricted to trusted accounts.
	const writes = windows.FILE_WRITE_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES |
		0x40 /* FILE_DELETE_CHILD */ | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER |
		windows.GENERIC_WRITE | windows.GENERIC_ALL
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return err
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("storage ancestor has an unsupported ACL entry")
		}
		if ace.Mask&writes != 0 && !trusted((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return errors.New("storage ancestor can be replaced by another user")
		}
	}
	return nil
}
