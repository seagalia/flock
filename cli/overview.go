package cli

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/seagalia/flock/cli/tui"
	"github.com/seagalia/flock/cli/tui/overview"
	"github.com/seagalia/flock/cli/tui/tuicommon"
	"github.com/seagalia/flock/sdk/clients"
)

type Overview struct {
	RefreshInterval uint `short:"r" name:"refresh" help:"Refresh interval in seconds" default:"90"`
	client          *clients.Client
	program         *tea.Program
	tuiApp          *tui.App
}

func (c *Overview) SetClient(client *clients.Client) {
	c.client = client
}

func (c *Overview) Run(ctx context.Context) error {
	app := tui.NewApp(overview.NewOverview(ctx, c.client))
	c.tuiApp = app
	p := tea.NewProgram(app, tea.WithContext(ctx))
	c.program = p

	// the program can end by itself (q), the reload loop then has to stop with it
	reloadCtx, stopReload := context.WithCancel(ctx)
	var reloader sync.WaitGroup
	reloader.Go(func() {
		c.AutoReload(reloadCtx)
	})
	_, err := p.Run()
	stopReload()
	reloader.Wait()
	if err != nil {
		return err
	}
	if app.Error() != nil {
		return app.Error()
	}
	return nil
}

func (c *Overview) AutoReload(ctx context.Context) {
	if c.RefreshInterval == 0 {
		return
	}
	ticker := time.NewTicker(time.Duration(c.RefreshInterval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.program.Send(tuicommon.ReloadMsg(true))
		}
	}
}
