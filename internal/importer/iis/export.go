package iis

// NormaliseStem and CleanToken expose the IIS route and token rules to the
// other web access-log importers (internal/importer/accesslog), so that a
// request path from nginx or Apache is reduced exactly as an IIS cs-uri-stem
// is. See normaliseStem and cleanToken.

// NormaliseStem turns a request target into a low-cardinality route; the query
// string and fragment are cut. ok is false for a target that is not a path.
func NormaliseStem(s string) (string, bool) { return normaliseStem(s) }

// CleanToken accepts short tokens made of [A-Za-z0-9._+-] only.
func CleanToken(s string, max int) (string, bool) { return cleanToken(s, max) }
