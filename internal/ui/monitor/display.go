package monitor

// mdState is the Monitor Display lane's per-monitor state (history dots,
// causes, commits). Owned by lane MD; keyed by job and build number.
type mdState struct{}
