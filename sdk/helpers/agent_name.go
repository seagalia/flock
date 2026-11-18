package helpers

import "regexp"

// AgentNamePattern is the protovalidate pattern of agent.Agent.name, an agent name is the CN of its client certificate
const AgentNamePattern = `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`

// AgentNameRule checks an agent name against AgentNamePattern
var AgentNameRule = regexp.MustCompile(AgentNamePattern)
