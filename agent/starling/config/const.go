package config

const (
	AppName = "starling"
	// the WAF runs embedded in the agent or as its own binary, its metrics show only where it runs
	WafAppName = "waf"
	// ConfigPatchEnv is shared with the F5 agent: whatever the agent, one variable patches its config
	ConfigPatchEnv = "FLOCK_AGENT_CONFIG_PATCH"
)
