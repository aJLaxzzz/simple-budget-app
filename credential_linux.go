package main

import (
	"budget/internal/storageguard"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// Used only for CREDENTIALS_DIRECTORY, never for BUDGET_PASSWORD_FILE.
// Inspect and read one open descriptor so path replacement cannot change the
// credential between ACL validation and reading. No libacl/helper dependency.
func readSystemdPasswordFile(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("systemd credential path must be absolute")
	}
	if _, err := storageguard.Resolve(filepath.Dir(path)); err != nil {
		return "", errors.New("invalid systemd credential directory")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("cannot open systemd credential")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Nlink != 1 || st.Size > maxPasswordBytes+2 || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
		return "", errors.New("invalid systemd credential file type, owner, link count or size")
	}
	acl := make([]byte, 4+5*8)
	n, aclErr := unix.Fgetxattr(fd, "system.posix_acl_access", acl)
	switch {
	case aclErr == nil:
		if st.Uid != 0 || st.Mode&0777 != 0440 || !validSystemdCredentialACL(acl[:n], uint32(os.Geteuid())) {
			return "", errors.New("systemd credential ACL grants unexpected access")
		}
	case errors.Is(aclErr, syscall.ENODATA), errors.Is(aclErr, syscall.ENOTSUP):
		if st.Mode&0077 != 0 {
			return "", errors.New("systemd credential without ACL must be owner-only")
		}
	default:
		return "", errors.New("cannot validate systemd credential ACL")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxPasswordBytes+3))
	if err != nil || len(raw) > maxPasswordBytes+2 {
		return "", errors.New("cannot read bounded systemd credential")
	}
	return passwordValue(raw)
}
