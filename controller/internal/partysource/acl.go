package partysource

import (
	"fmt"
	"regexp"
)

const (
	systemSID         = "S-1-5-18"
	administratorsSID = "S-1-5-32-544"
)

// readACL returns the controller's user SID and the SID of every principal
// the file's DACL grants any access to the file itself. Tests replace it and
// lookupName (accountName).
var readACL, lookupName = fileACL, accountName

// wellKnownSID matches SIDs Windows names locally; naming a domain account
// asks a domain controller and can stall until its RPC timeout.
var wellKnownSID = regexp.MustCompile(`^S-1-[123]-\d+$|^S-1-5-\d+$|^S-1-5-32-\d+$`)

// checkTokenACL refuses a token file that grants access to anyone but this
// user, SYSTEM and Administrators, naming the file and the first other.
func checkTokenACL(path string) error {
	user, granted, e := readACL(path)
	if e != nil {
		return fmt.Errorf("Home Assistant token file permissions unreadable: %w", e)
	}
	for _, sid := range granted {
		if sid == user || sid == systemSID || sid == administratorsSID {
			continue
		}
		who := sid
		if wellKnownSID.MatchString(sid) {
			if name := lookupName(sid); name != "" {
				who = name + " (" + sid + ")"
			}
		}
		return fmt.Errorf("Home Assistant token file %s also grants access to %s; allow only this user, SYSTEM and Administrators", path, who)
	}
	return nil
}
