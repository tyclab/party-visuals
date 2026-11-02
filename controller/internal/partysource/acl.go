package partysource

import (
	"fmt"
	"regexp"
)

const (
	systemSID         = "S-1-5-18"
	administratorsSID = "S-1-5-32-544"
)

// readACL returns the controller's user SID and every principal the file's
// DACL grants any access to the file itself; Conditional marks a callback
// entry, whose condition is not evaluated. Tests replace it and lookupName.
var readACL, lookupName = fileACL, accountName

type grant struct {
	SID         string
	Conditional bool
}

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
	for _, g := range granted {
		if g.SID == user || g.SID == systemSID || g.SID == administratorsSID {
			continue
		}
		who := g.SID
		if wellKnownSID.MatchString(g.SID) {
			if name := lookupName(g.SID); name != "" {
				who = name + " (" + g.SID + ")"
			}
		}
		if g.Conditional {
			return fmt.Errorf("Home Assistant token file %s has a conditional entry for %s, which the controller does not evaluate; allow only this user, SYSTEM and Administrators", path, who)
		}
		return fmt.Errorf("Home Assistant token file %s also grants access to %s; allow only this user, SYSTEM and Administrators", path, who)
	}
	return nil
}
