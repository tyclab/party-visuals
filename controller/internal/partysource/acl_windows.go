//go:build windows

package partysource

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ACE types beside ACCESS_ALLOWED and ACCESS_DENIED whose SID follows the
// access mask, as in ACCESS_ALLOWED_ACE.
const (
	accessAllowedCallback = 9
	accessDeniedCallback  = 10
)

const everyoneSID = "S-1-1-0"

func fileACL(path string) (string, []string, error) {
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return "", nil, e
	}
	sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return "", nil, e
	}
	dacl, _, e := sd.DACL()
	if errors.Is(e, windows.ERROR_OBJECT_NOT_FOUND) || (e == nil && dacl == nil) {
		// A missing or NULL DACL lets everyone in.
		return user.User.Sid.String(), []string{everyoneSID}, nil
	}
	if e != nil {
		return "", nil, e
	}
	var granted []string
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(dacl, i, &ace); e != nil {
			return "", nil, e
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE, accessDeniedCallback:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE, accessAllowedCallback:
		default:
			return "", nil, fmt.Errorf("unexpected ACE type %d", ace.Header.AceType)
		}
		if ace.Mask == 0 || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		granted = append(granted, (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String())
	}
	return user.User.Sid.String(), granted, nil
}

func accountName(sid string) string {
	s, e := windows.StringToSid(sid)
	if e != nil {
		return ""
	}
	account, domain, _, e := s.LookupAccount("")
	if e != nil || account == "" {
		return ""
	}
	if domain == "" {
		return account
	}
	return domain + `\` + account
}
