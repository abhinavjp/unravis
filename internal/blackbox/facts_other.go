//go:build !windows

package blackbox

const factsSupported = false

func readFactsPlatform() *FactsGroup { return nil }
