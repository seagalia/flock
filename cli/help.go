package cli

import "github.com/alecthomas/kong"

type HelpCmd struct {
}

func (c *HelpCmd) Run(ctx *kong.Context) error {
	// The context given to Run is the one of the help command, tracing no arguments gives the root one.
	rootCtx, err := kong.Trace(ctx.Kong, nil)
	if err != nil {
		return err
	}
	return rootCtx.PrintUsage(false)
}

func (c *HelpCmd) NoCheckUpdate() bool {
	return true
}
