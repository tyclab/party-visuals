package main

import "testing"

func TestWindowsReportsTheNotificationIconSize(t *testing.T) {
	small := smallIconSize()
	if small < 16 || small > 256 {
		t.Fatalf("SM_CXSMICON at the system DPI is %d", small)
	}
	t.Logf("small icon %d px, tray PNG %d px", small, traySize(small))
}
