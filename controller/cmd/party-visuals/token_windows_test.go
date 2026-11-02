//go:build windows

package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

// ownerOnly gives path a protected DACL for this user alone, so the token
// check does not depend on the host's temporary folder.
func ownerOnly(t *testing.T, path string) {
	t.Helper()
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if e != nil {
		t.Fatal(e)
	}
	dacl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); e != nil {
		t.Fatal(e)
	}
}
