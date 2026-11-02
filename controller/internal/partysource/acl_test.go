package partysource

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testUser = "S-1-5-21-1000000001-1000000002-1000000003-1001"

// fakeACL stands in for the token file's DACL.
func fakeACL(t *testing.T, granted []grant, e error) {
	t.Helper()
	saved := readACL
	readACL = func(string) (string, []grant, error) { return testUser, granted, e }
	t.Cleanup(func() { readACL = saved })
}
func grants(sids ...string) []grant {
	g := make([]grant, len(sids))
	for i, sid := range sids {
		g[i].SID = sid
	}
	return g
}

func TestATokenFileOtherAccountsCanOpenIsRefusedBeforeAnyRequest(t *testing.T) {
	const users = "S-1-5-32-545"
	r := newRig(t, "example-token", reply{state: "3"})
	fakeACL(t, grants(testUser, systemSID, administratorsSID, users), nil)
	if e := r.run(); e == nil {
		t.Fatal("token accepted")
	}
	commands, _, requests := r.snapshot()
	if requests != 0 || len(commands) != 0 {
		t.Fatalf("%d requests, commands %v", requests, commands)
	}
	if s := r.source.Status(); s.State != "refused" || !strings.Contains(s.Summary, users) {
		t.Fatalf("status %+v", s)
	}
	file := r.source.config.TokenFile
	if len(r.logs) != 1 || !strings.Contains(r.logs[0], file) || !strings.Contains(r.logs[0], users) || strings.Contains(r.logs[0], "example-token") {
		t.Fatalf("want one line naming %s and %s, got %q", file, users, r.logs)
	}
}

func TestATokenFileOnlyThisUserSystemAndAdministratorsCanOpenIsUsed(t *testing.T) {
	r := newRig(t, "example-token", reply{state: "3"})
	fakeACL(t, grants(testUser, systemSID, administratorsSID), nil)
	if e := r.run(); e != nil {
		t.Fatal(e)
	}
	if commands, _, _ := r.snapshot(); !reflect.DeepEqual(commands, []string{"party-on"}) {
		t.Fatalf("commands %v", commands)
	}
}

func TestUnreadableTokenFilePermissionsRefuseTheToken(t *testing.T) {
	r := newRig(t, "example-token", reply{state: "3"})
	fakeACL(t, nil, errors.New("Access is denied."))
	if e := r.run(); e == nil || !strings.Contains(e.Error(), "permissions unreadable") {
		t.Fatalf("run: %v", e)
	}
	if _, _, requests := r.snapshot(); requests != 0 {
		t.Fatalf("%d requests", requests)
	}
}

func TestOnlyWellKnownPrincipalsAreLookedUpByName(t *testing.T) {
	saved := lookupName
	var asked []string
	lookupName = func(sid string) string { asked = append(asked, sid); return `BUILTIN\Users` }
	t.Cleanup(func() { lookupName = saved })
	const domainUser = "S-1-5-21-1000000001-1000000002-1000000003-1105"
	fakeACL(t, grants(testUser, domainUser), nil)
	if _, e := ReadToken(writeToken(t)); e == nil || !strings.Contains(e.Error(), "access to "+domainUser+";") || len(asked) != 0 {
		t.Fatalf("a domain account: %v, looked up %v", e, asked)
	}
	fakeACL(t, grants(testUser, "S-1-5-32-545"), nil)
	if _, e := ReadToken(writeToken(t)); e == nil || !strings.Contains(e.Error(), `BUILTIN\Users (S-1-5-32-545)`) || !reflect.DeepEqual(asked, []string{"S-1-5-32-545"}) {
		t.Fatalf("a builtin group: %v, looked up %v", e, asked)
	}
}

func writeToken(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ha.token")
	if e := os.WriteFile(p, []byte("example-token"), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestAConditionalEntryIsRefusedAsNotEvaluated(t *testing.T) {
	fakeACL(t, []grant{{SID: testUser}, {SID: "S-1-1-0", Conditional: true}}, nil)
	_, e := ReadToken(writeToken(t))
	if e == nil || !strings.Contains(e.Error(), "conditional entry for") || !strings.Contains(e.Error(), "S-1-1-0") || !strings.Contains(e.Error(), "does not evaluate") {
		t.Fatalf("conditional entry: %v", e)
	}
	fakeACL(t, []grant{{SID: testUser, Conditional: true}, {SID: systemSID, Conditional: true}}, nil)
	if _, e = ReadToken(writeToken(t)); e != nil {
		t.Fatalf("conditional entries for allowed accounts: %v", e)
	}
}
