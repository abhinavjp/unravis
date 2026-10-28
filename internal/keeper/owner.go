package keeper

import (
	"crypto/sha1"
	"encoding/binary"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ServiceName is the Windows service name the installer registers for the key keeper.
const ServiceName = "UnravisKeeper"

// ServiceSID is the per-service SID Windows gives a service (NT SERVICE\<name>): S-1-5-80 followed by the SHA-1 of the upper-case
// service name in UTF-16LE, read as five little-endian 32-bit numbers. Only the service control manager puts it in a token.
func ServiceSID(name string) string {
	units := utf16.Encode([]rune(strings.ToUpper(name)))
	raw := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(raw[2*i:], u)
	}
	sum := sha1.Sum(raw)
	var b strings.Builder
	b.WriteString("S-1-5-80")
	for i := 0; i < 5; i++ {
		b.WriteByte('-')
		b.WriteString(strconv.FormatUint(uint64(binary.LittleEndian.Uint32(sum[4*i:])), 10))
	}
	return b.String()
}

// trustedPipeOwner reports whether owner may own the keeper's pipe: LocalSystem, LocalService, NetworkService, Administrators (the
// owner of objects a LocalSystem service creates) or the keeper's own service SID. A standard user cannot create an object with any of
// these as its owner; an administrator can, which is the stated administrator limit (security.md). extra lists SIDs a test also trusts.
func trustedPipeOwner(owner string, extra []string) bool {
	switch owner {
	case "S-1-5-18", "S-1-5-19", "S-1-5-20", "S-1-5-32-544":
		return true
	}
	if owner != "" && owner == ServiceSID(ServiceName) {
		return true
	}
	for _, e := range extra {
		if owner != "" && owner == e {
			return true
		}
	}
	return false
}
