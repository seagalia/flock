package app

import (
	"fmt"
	"strings"
	"sync"

	"github.com/seagalia/flock/agent/starling/agentlist"
	"github.com/seagalia/flock/agent/starling/layer2"
	"github.com/seagalia/flock/agent/starling/nm"
)

func (a *App) loadAdvertisement() error {
	err := a.loadNetworkManager()
	if err != nil {
		return fmt.Errorf("failed to load network manager: %w", err)
	}

	err = a.loadAgentList()
	if err != nil {
		return fmt.Errorf("failed to load agent list: %w", err)
	}

	err = a.loadVipPlacer()
	if err != nil {
		return fmt.Errorf("failed to load vip placer: %w", err)
	}

	a.loadL2Announcer()

	err = a.postLoad()
	if err != nil {
		return fmt.Errorf("post load advertisement: %w", err)
	}
	return nil
}

func (a *App) postLoad() error {
	// dev mode runs on a laptop whose nftables belong to someone else
	if a.cnf.DevMode {
		return nil
	}
	if a.cnf.AdvertisementConfig != nil && !a.cnf.AdvertisementConfig.DisableSnat {
		err := nm.SnatInterfaces(*a.cnf.AdvertisementConfig.NetworkManager, a.cnf.AdvertisementConfig.Interfaces)
		if err != nil {
			return fmt.Errorf("failed to enable snat: %w", err)
		}
	}

	return nil
}

func (a *App) loadNetworkManager() error {
	var err error
	if a.cnf.DevMode {
		a.netman = nm.NewLogNetworkManager()
		return nil
	}
	a.netman, err = nm.NewNetworkManager(*a.cnf.AdvertisementConfig.NetworkManager, a.ipWatcher)
	if err != nil {
		return err
	}
	return nil
}

func (a *App) loadAgentList() error {
	if a.cnf.AdvertisementConfig == nil || a.cnf.AdvertisementConfig.AgentList == nil {
		return nil
	}
	var err error
	a.agentList, err = agentlist.New(
		a.name,
		a.cnf.AdvertisementConfig.AgentList.ListenHost,
		a.cnf.AdvertisementConfig.AgentList.ListenPort,
		a.cnf.AdvertisementConfig.AgentList.Secret,
		a.cnf.AdvertisementConfig.AgentList.Members,
	)
	if err != nil {
		return err
	}
	return nil
}

func (a *App) loadVipPlacer() error {
	if a.agentList == nil {
		return nil
	}

	placStratRaw := ""
	if a.cnf.AdvertisementConfig != nil {
		placStratRaw = a.cnf.AdvertisementConfig.DynamicPlacementStrategy
	}
	placStrat, ok := agentlist.GetPlacerStrat(placStratRaw)
	if !ok {
		return fmt.Errorf(
			"invalid placement strategy `%s`, available strategies are `%s`",
			placStratRaw,
			strings.Join(agentlist.GetPlacerStrats(), "`, `"),
		)
	}
	a.vipPlacer = agentlist.NewVipPlacer(a.ctx, a.name, a.agentList, placStrat, a.rt.Store.VIPs())
	return nil
}

func (a *App) loadL2Announcer() {
	if a.cnf.AdvertisementConfig.L2Config == nil {
		return
	}
	a.l2Announcer = layer2.New(a.cnf.AdvertisementConfig.L2Config.DisableGratuitous)
}

func (a *App) RunAnnouncers(wg *sync.WaitGroup) {
	if a.l2Announcer != nil {
		wg.Go(func() { a.l2Announcer.Run(a.announceCtx) })
	}
	if a.agentList != nil {
		wg.Go(func() {
			err := a.agentList.Run(a.announceCtx)
			if err != nil {
				a.entry.Error().Err(err).Msg("Agent list stopped with an error")
			}
		})
	}
}
