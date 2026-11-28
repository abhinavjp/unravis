package keeper

import "api-audit-explorer/internal/keeperwire"

// ServiceName is the Windows service name the installer registers for the key keeper.
const ServiceName = keeperwire.ServiceName

// ServiceSID is the per-service SID Windows gives a service (NT SERVICE\<name>). See keeperwire.ServiceSID.
func ServiceSID(name string) string { return keeperwire.ServiceSID(name) }

// trustedPipeOwner reports whether owner may own the keeper's pipe. See keeperwire.TrustedPipeOwner.
func trustedPipeOwner(owner string, extra []string) bool {
	return keeperwire.TrustedPipeOwner(owner, extra)
}
