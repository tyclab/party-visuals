//go:build windows

package partysource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestTheTokenFileDACLAdmitsOnlyThisUserSystemAndAdministrators(t *testing.T) {
	token, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	user := token.User.Sid.String()
	file := filepath.Join(t.TempDir(), "ha.token")
	if e = os.WriteFile(file, []byte("example-token"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadToken(file); e != nil {
		t.Fatalf("a token file in this user's temporary folder: %v", e)
	}
	const opened, conditional = "also grants access to", "conditional entry for"
	for _, c := range []struct{ name, sddl, refusal, sid string }{
		{"user, SYSTEM and Administrators", "D:P(A;;FA;;;" + user + ")(A;;FA;;;SY)(A;;FA;;;BA)", "", ""},
		{"a deny for Guests grants nothing", "D:P(D;;FA;;;BG)(A;;FA;;;" + user + ")", "", ""},
		{"Users may read", "D:P(A;;FA;;;" + user + ")(A;;FR;;;BU)", opened, "S-1-5-32-545"},
		{"Everyone may read", "D:P(A;;FA;;;" + user + ")(A;;FR;;;WD)", opened, "S-1-1-0"},
		{"Everyone may read if an administrator", "D:P(A;;FA;;;" + user + ")(XA;;FR;;;WD;(Member_of {SID(BA)}))", conditional, "S-1-1-0"},
		{"no DACL", "D:NO_ACCESS_CONTROL", opened, "S-1-1-0"},
	} {
		sd, e := windows.SecurityDescriptorFromString(c.sddl)
		if e != nil {
			t.Fatal(e)
		}
		dacl, _, e := sd.DACL()
		if e != nil {
			t.Fatal(e)
		}
		if e = windows.SetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); e != nil {
			t.Fatalf("%s: %v", c.name, e)
		}
		_, e = ReadToken(file)
		switch {
		case c.refusal == "" && e != nil:
			t.Errorf("%s: refused: %v", c.name, e)
		case c.refusal != "" && (e == nil || !strings.Contains(e.Error(), c.refusal) || !strings.Contains(e.Error(), c.sid) || !strings.Contains(e.Error(), file)):
			t.Errorf("%s: want %q naming %s and the file, got %v", c.name, c.refusal, c.sid, e)
		}
	}
}
