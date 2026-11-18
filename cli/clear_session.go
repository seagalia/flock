package cli

import (
	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/cli/app"
)

type ClearSessionCmd struct {
}

func (c *ClearSessionCmd) Run() error {
	app.ClearSession(ExpandConfigPath())
	msg.Successf("Session and token cleared")
	return nil
}
