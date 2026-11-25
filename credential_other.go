//go:build !linux

package main

// Local development retains the strict private-file policy. Linux credentials
// with POSIX ACLs are handled only by the Linux implementation.
func readSystemdPasswordFile(path string) (string, error) {
	return readPasswordFile(path)
}
