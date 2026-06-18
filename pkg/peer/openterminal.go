package peer

// PlanStream decides per-terminal setup from orthogonal host/dialer roles.
// dial=true means /ws/peer-stream gets dialed locally; bridge=true means this
// node owns PTY bridge work.
func PlanStream(amHost, amDialer bool) (dial, bridge bool) {
	return amDialer, amHost
}
