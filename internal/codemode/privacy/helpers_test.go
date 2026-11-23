package privacy

// Test shorthands for the host side of the Guard with no identifiers of its own (a test learns through g.sw.learn first).

func (g *Guard) Result(raw []byte) ([]byte, error) { return g.HostResult(raw, IDs{}) }
func (g *Guard) Error(msg string) string           { return g.HostError(msg, IDs{}) }
