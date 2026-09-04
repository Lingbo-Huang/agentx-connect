//go:build !windows

package hostauth

import (
	"errors"
	"os"
)

func validateCredentialPermissions(_ string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("HostInstallation credential file must not be readable or writable by group or others")
	}
	return nil
}

func secureCredentialFile(file *os.File) error { return file.Chmod(0o600) }
