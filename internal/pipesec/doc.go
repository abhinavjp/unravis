// Package pipesec is the only named-pipe listener and client of Unravis (BL-330, spec 02 section 3.1).
//
// The server side creates a pool of pipe instances that nobody can squat (first instance flag, full instance pool), that remote
// machines cannot reach (PIPE_REJECT_REMOTE_CLIENTS), whose DACL names only the current logon SID, and it checks every peer (logon
// SID, session, image path) before Accept returns it. The client side (DialVerified) checks the server the same way before any byte
// is sent. The runtime is Windows-only; policy.go and names.go are portable so the rules are unit-tested on every OS, and the other
// builds return ErrUnsupported.
package pipesec
