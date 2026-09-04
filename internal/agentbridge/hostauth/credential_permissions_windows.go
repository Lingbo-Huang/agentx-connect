package hostauth

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Chmod cannot express Windows confidentiality. Protect the DACL before any
// credential bytes are written; only the current user's SID receives access.
func secureCredentialFile(file *os.File) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return errors.New("resolve credential file owner")
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return errors.New("prepare private credential ACL")
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return errors.New("prepare private credential ACL")
	}
	if err := windows.SetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return errors.New("protect credential file ACL")
	}
	return nil
}

func validateCredentialPermissions(path string, _ os.FileInfo) error {
	fail := errors.New("HostInstallation credential file must have a private current-user Windows ACL")
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fail
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fail
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return fail
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return fail
	}
	allowed := false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil || ace == nil {
			return fail
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fail
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || !sid.Equals(user.User.Sid) {
			return fail
		}
		allowed = true
	}
	if !allowed {
		return fail
	}
	return nil
}
