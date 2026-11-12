package monitor

// maState is the Monitor Actions lane's per-monitor state (test reports,
// notified builds). Owned by lane MA; keyed by job and build number.
type maState struct{}
