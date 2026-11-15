package worker

// Placement is where a session's worker opened the visuals and why, and the
// display power reading the session opened with.
type Placement struct {
	Text            string `json:"text"`
	PreferredListed bool   `json:"preferredListed"`
	Power           string `json:"power"`
}

// Stale reports whether the visuals would open on another screen with the
// reading now: only while Windows lists the preferred display, and only when
// on or off changed. Like the worker, it counts a missing reading as on.
func (p Placement) Stale(now string) bool {
	return p.PreferredListed && (p.Power == "off") != (now == "off")
}

// Placement is the current session's, empty until its worker reports one.
func (b *Backend) Placement() Placement {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.placed
}
func (b *Backend) place(p Placement) {
	b.mu.Lock()
	b.placed = p
	b.mu.Unlock()
}
func (b *Backend) power() string {
	if b.Power == nil {
		return "none"
	}
	return b.Power()
}
func (b *Backend) command(power string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", b.Config.WorkerScript, "-ConfigPath", b.ConfigPath, "-DisplayPower", power}
}
