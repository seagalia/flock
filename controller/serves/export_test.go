package serves

import "time"

// ShortenAgentCallTimeout makes the agent call timeout short and returns the function restoring it
func ShortenAgentCallTimeout(d time.Duration) func() {
	previous := agentCallTimeout
	agentCallTimeout = d
	return func() { agentCallTimeout = previous }
}
