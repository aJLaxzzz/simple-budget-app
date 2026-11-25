package main

import (
	"encoding/binary"
	"testing"
)

func credentialACLFixture(uid uint32) []byte {
	raw := make([]byte, 44)
	binary.LittleEndian.PutUint32(raw, 2)
	for i, tag := range []uint16{1, 2, 4, 16, 32} {
		e := raw[4+i*8:]
		binary.LittleEndian.PutUint16(e, tag)
		binary.LittleEndian.PutUint16(e[2:], []uint16{4, 4, 0, 4, 0}[i])
		id := ^uint32(0)
		if tag == 2 {
			id = uid
		}
		binary.LittleEndian.PutUint32(e[4:], id)
	}
	return raw
}

func TestSystemdCredentialACL(t *testing.T) {
	// Exact metadata observed on Ubuntu 24.04 / systemd 255: owner root,
	// mode 0440, ACL user 995:r--, group::---, mask::r--, other::---.
	raw := credentialACLFixture(995)
	if !validSystemdCredentialACL(raw, 995) {
		t.Fatal("legitimate systemd ACL rejected")
	}
	if validSystemdCredentialACL(raw, 994) || validSystemdCredentialACL(raw, 0) {
		t.Fatal("wrong service UID accepted")
	}
	// No extra user/group entries, unknown version, write/execute permission,
	// noncanonical IDs or truncated ACLs may broaden access.
	for n := 0; n < len(raw); n++ {
		if validSystemdCredentialACL(raw[:n], 995) {
			t.Fatalf("truncated ACL accepted: %d", n)
		}
	}
	if validSystemdCredentialACL(append(append([]byte{}, raw...), make([]byte, 8)...), 995) {
		t.Fatal("extra ACL entry accepted")
	}
	for i := range raw {
		for bit := byte(1); bit != 0; bit <<= 1 {
			changed := append([]byte{}, raw...)
			changed[i] ^= bit
			if validSystemdCredentialACL(changed, 995) {
				t.Fatalf("unexpected ACL accepted at byte %d", i)
			}
		}
	}
}
