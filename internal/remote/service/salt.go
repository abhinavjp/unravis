package service

import (
	"context"
	"errors"
	"slices"

	"api-audit-explorer/internal/remote/registry"
	"api-audit-explorer/internal/remote/saltmaster"
	"api-audit-explorer/internal/remote/transport"
)

// The Salt way in: Unravis signs in to one jump host over SSH (the same pinned host key, the same sealed secret as any server), runs the
// master's read-only commands there, and reads one machine's files through them. The machines it may read are the ones the person ticked
// (registry.Entry.MinionIDs). Nothing is ever written to the master or to a machine. This path is tested against a fake master only; it has
// not been run against a real one.

// Plain sentences the screen shows as they are.
var (
	ErrNoSalt           = errors.New("This server is not set up with a Salt master, so there are no machines behind it.")   //nolint:staticcheck // shown to the person as a sentence
	ErrNeedMachine      = errors.New("Choose which machine to read first.")                                                 //nolint:staticcheck // shown to the person as a sentence
	ErrMachineNotListed = errors.New("That machine is not on this server's list. Add it to the machines you ticked first.") //nolint:staticcheck // shown to the person as a sentence
	ErrNoExec           = errors.New("This connection cannot run commands on the jump host.")                               //nolint:staticcheck // shown to the person as a sentence
)

func hasSalt(e registry.Entry) bool { return hasTransport(e, registry.TransportSalt) }

// hasJump says the server signs in over SSH: directly, or as the jump host in front of a Salt master.
func hasJump(e registry.Entry) bool { return hasTransport(e, registry.TransportSSH) || hasSalt(e) }

// MachineView is one machine the master knows, and whether the person ticked it for this server.
type MachineView struct {
	saltmaster.Machine
	Chosen bool `json:"chosen"`
}

// Machines lists the machines the master has keys for and which of them answer. It signs in to the jump host and changes nothing.
func (s *Service) Machines(ctx context.Context, id string) ([]MachineView, error) {
	e, err := s.entry(id)
	if err != nil {
		return nil, err
	}
	if !hasSalt(e) {
		return nil, ErrNoSalt
	}
	cn, err := s.dial(ctx, e)
	if err != nil {
		return nil, err
	}
	defer cn.Close() //nolint:errcheck // best-effort cleanup, nothing to do on failure
	m, err := master(e, cn)
	if err != nil {
		return nil, err
	}
	list, err := m.Machines(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MachineView, 0, len(list))
	for _, mc := range list {
		out = append(out, MachineView{Machine: mc, Chosen: slices.Contains(e.MinionIDs, mc.ID)})
	}
	return out, nil
}

// master builds the command runner for the jump host from a signed-in connection.
func master(e registry.Entry, cn Conn) (saltmaster.Master, error) {
	r, ok := cn.(saltmaster.Runner)
	if !ok {
		return saltmaster.Master{}, ErrNoExec
	}
	return saltmaster.Master{R: r, OS: e.OS, Sudo: e.SaltSudo && e.OS != "windows"}, nil
}

// dial signs in to the server's SSH host.
func (s *Service) dial(ctx context.Context, e registry.Entry) (Conn, error) {
	c, err := s.signIn(e)
	if err != nil {
		return nil, err
	}
	return s.d.Connect(ctx, c)
}

// target is one place to read: the server itself over SSH, or one machine behind its Salt master.
type target struct {
	e       registry.Entry
	tr      transport.Transport
	conn    Conn
	machine string // empty for a direct SSH read
}

func (t *target) close() { _ = t.conn.Close() }

// pick settles which machine a Salt request reads. A server with one ticked machine needs none named. The machine must be one the person
// ticked; no other name is ever sent to the master.
func pick(e registry.Entry, machine string) (string, error) {
	switch {
	case machine == "" && len(e.MinionIDs) == 1:
		return e.MinionIDs[0], nil
	case machine == "":
		return "", ErrNeedMachine
	case !slices.Contains(e.MinionIDs, machine):
		return "", ErrMachineNotListed
	}
	return machine, nil
}

// open signs in and returns what to read from. A Salt server reads a machine when one is named (or is the only one); a server with SSH
// and no machine named is read directly.
func (s *Service) open(ctx context.Context, id, machine string) (*target, error) {
	e, err := s.entry(id)
	if err != nil {
		return nil, err
	}
	viaSalt := hasSalt(e) && (machine != "" || !hasTransport(e, registry.TransportSSH))
	if !viaSalt && machine != "" {
		return nil, ErrNoSalt
	}
	if viaSalt {
		if machine, err = pick(e, machine); err != nil {
			return nil, err
		}
	}
	cn, err := s.dial(ctx, e)
	if err != nil {
		return nil, err
	}
	if !viaSalt {
		return &target{e: e, tr: cn, conn: cn}, nil
	}
	m, err := master(e, cn)
	if err != nil {
		_ = cn.Close()
		return nil, err
	}
	ident, err := m.Identify(ctx, machine)
	if err != nil {
		_ = cn.Close()
		return nil, err
	}
	tr, err := m.For(machine, ident.OS, allowed(e))
	if err != nil {
		_ = cn.Close()
		return nil, err
	}
	return &target{e: e, tr: tr, conn: cn, machine: machine}, nil
}
