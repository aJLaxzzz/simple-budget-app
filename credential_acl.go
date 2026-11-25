package main

import "encoding/binary"

// Linux POSIX ACL v2. A group mode bit is the ACL mask, not necessarily a
// permission granted to the owning group. Accept only systemd's exact shape:
// root can read, this service UID can read, no group/other access or write bits.
func validSystemdCredentialACL(raw []byte, uid uint32) bool {
	if uid == 0 || len(raw) != 4+5*8 || binary.LittleEndian.Uint32(raw) != 2 {
		return false
	}
	tags := [...]uint16{1, 2, 4, 16, 32} // USER_OBJ, USER, GROUP_OBJ, MASK, OTHER
	perms := [...]uint16{4, 4, 0, 4, 0}
	for i, tag := range tags {
		e := raw[4+i*8 : 4+(i+1)*8]
		id := ^uint32(0)
		if tag == 2 {
			id = uid
		}
		if binary.LittleEndian.Uint16(e) != tag || binary.LittleEndian.Uint16(e[2:]) != perms[i] || binary.LittleEndian.Uint32(e[4:]) != id {
			return false
		}
	}
	return true
}
