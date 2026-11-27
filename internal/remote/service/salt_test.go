package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/remote/pull"
	"api-audit-explorer/internal/remote/registry"
	"api-audit-explorer/internal/remote/saltmaster"
	"api-audit-explorer/internal/remote/sshdirect"
)

// saltConn is a signed-in jump host that also answers the master's read-only commands for one Linux machine.
type saltConn struct {
	*fakeConn
	cmds []string
	down bool
}

func (c *saltConn) Exec(_ context.Context, cmd string, _ int) ([]byte, error) {
	c.cmds = append(c.cmds, cmd)
	switch {
	case strings.Contains(cmd, "salt-key"):
		return []byte(`{"minions":["web-01","db-01"],"minions_pre":[],"minions_denied":[],"minions_rejected":[]}`), nil
	case strings.Contains(cmd, "salt-run"):
		return []byte(`{"up":["web-01"],"down":["db-01"]}`), nil
	case c.down:
		return []byte(`{"web-01":"Minion did not return. [Not connected]"}`), nil
	case strings.Contains(cmd, "'test.ping'"):
		return []byte(`{"web-01":true}`), nil
	case strings.Contains(cmd, "'grains.item'"):
		return []byte(`{"web-01":{"kernel":"Linux","osfullname":"Ubuntu","osrelease":"22.04"}}`), nil
	case strings.Contains(cmd, "date +%s"):
		return []byte(`{"web-01":"1700000000"}`), nil
	case strings.Contains(cmd, "realpath -e"):
		return []byte(`{"web-01":"/var/log/blackbox"}`), nil
	case strings.Contains(cmd, "ls -l"):
		return []byte(`{"web-01":"total 4\n-rw-r--r-- 1 root root 12 1700000100 bb-20261126-07.ndjson"}`), nil
	}
	return nil, errors.New("fake master: unexpected command: " + cmd)
}

var _ saltmaster.Runner = (*saltConn)(nil)

func saltRig(t *testing.T, minions ...string) (*rig, *saltConn) {
	t.Helper()
	r := newRig(t, func(in *registry.AddInput) {
		in.Name, in.OS = "jump-01", "linux"
		in.Transports = []string{registry.TransportSalt}
		in.MinionIDs = minions
		in.SaltSudo = true
		in.Folders = map[string][]string{registry.FolderBlackbox: {"/var/log/blackbox"}}
		in.BlackboxID = ""
	})
	r.pin(t)
	c := &saltConn{fakeConn: r.conn}
	r.svc.d.Connect = func(_ context.Context, cfg sshdirect.Config) (Conn, error) {
		r.cfgSeen = append(r.cfgSeen, cfg)
		return c, nil
	}
	return r, c
}

func TestMachinesMarksTheTickedOnes(t *testing.T) {
	r, c := saltRig(t, "web-01")
	got, err := r.svc.Machines(context.Background(), r.id)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, m := range got {
		if m.Chosen != (m.ID == "web-01") {
			t.Errorf("%s chosen=%v", m.ID, m.Chosen)
		}
	}
	if !c.fakeConn.closed {
		t.Error("the connection stayed open")
	}
	if !strings.HasPrefix(c.cmds[0], "sudo -n ") {
		t.Errorf("sudo not applied: %s", c.cmds[0])
	}
}

func TestMachinesNeedsSalt(t *testing.T) {
	r := newRig(t, nil)
	if _, err := r.svc.Machines(context.Background(), r.id); !errors.Is(err, ErrNoSalt) {
		t.Fatalf("got %v", err)
	}
}

func TestSaltTestAndBrowseReadTheMachine(t *testing.T) {
	r, c := saltRig(t, "web-01")
	ctx := context.Background()
	res, err := r.svc.Test(ctx, r.id, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Machine != "web-01" || res.Host.OS != "linux" {
		t.Fatalf("%+v", res)
	}
	files, err := r.svc.Browse(ctx, r.id, "web-01", "/var/log/blackbox")
	if err != nil || len(files) != 1 || files[0].Name != "bb-20261126-07.ndjson" {
		t.Fatalf("%+v %v", files, err)
	}
	if !c.fakeConn.closed {
		t.Error("the connection stayed open")
	}
}

func TestSaltNeverReadsAMachineThatWasNotTicked(t *testing.T) {
	r, c := saltRig(t, "web-01", "web-02")
	ctx := context.Background()
	if _, err := r.svc.Browse(ctx, r.id, "", "/var/log/blackbox"); !errors.Is(err, ErrNeedMachine) {
		t.Errorf("no machine named with two ticked: %v", err)
	}
	if _, err := r.svc.Browse(ctx, r.id, "db-01", "/var/log/blackbox"); !errors.Is(err, ErrMachineNotListed) {
		t.Errorf("unticked machine: %v", err)
	}
	if _, err := r.svc.Browse(ctx, r.id, "*", "/var/log/blackbox"); !errors.Is(err, ErrMachineNotListed) {
		t.Errorf("wildcard: %v", err)
	}
	if len(c.cmds) != 0 {
		t.Fatalf("a refused machine reached the master: %v", c.cmds)
	}
	if len(r.cfgSeen) != 0 {
		t.Fatal("a refused machine caused a sign-in")
	}
}

func TestSaltOfflineMachineIsAnError(t *testing.T) {
	r, c := saltRig(t, "web-01")
	c.down = true
	_, err := r.svc.Browse(context.Background(), r.id, "web-01", "/var/log/blackbox")
	if !errors.Is(err, saltmaster.ErrOffline) {
		t.Fatalf("got %v", err)
	}
	if !c.fakeConn.closed {
		t.Error("the connection stayed open after a failure")
	}
}

func TestMachineOnAServerWithoutSalt(t *testing.T) {
	r := newRig(t, nil)
	r.pin(t)
	if _, err := r.svc.Browse(context.Background(), r.id, "web-01", `D:\Blackbox`); !errors.Is(err, ErrNoSalt) {
		t.Fatalf("got %v", err)
	}
}

func TestSaltPlanListsThroughTheMachine(t *testing.T) {
	r, _ := saltRig(t, "web-01")
	plan, err := r.svc.Plan(context.Background(), r.id, "", pull.Request{Kind: "blackbox", Dir: "/var/log/blackbox"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || plan.Files[0].Name != "bb-20261126-07.ndjson" {
		t.Fatalf("%+v", plan)
	}
}
