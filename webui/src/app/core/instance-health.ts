import { computed, DOCUMENT, effect, inject, Injectable, signal, untracked } from '@angular/core';
import { create } from '@bufbuild/protobuf';
import { anyUnpack } from '@bufbuild/protobuf/wkt';
import { ContextValues, createContextValues } from '@connectrpc/connect';
import {
  BackendEndpointStats,
  BackendEndpointStatsSchema,
} from '../../gen/flock/api/config/backend/v1/backend_pb';
import { Instance } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import {
  GetAgentStatusResponse,
  InstanceNode,
  StatusResponse,
} from '../../gen/flock/services/lookup/v1/lookup_pb';
import { agentsReport } from './agents';
import { Api, silentErrors } from './api';
import { HealthLevel, vsBackendUuids } from './display';
import { Instances } from './instances';
import { kinds, linkedToFilter, listAll, typeUrl } from './resources';

export const refreshMs = 30_000;

export interface Health {
  healthy: number;
  total: number;
}

// mergeEndpointStats gives the endpoints of each backend, every agent reports its own view of them
export function mergeEndpointStats(statuses: StatusResponse[]): Map<string, BackendEndpointStats> {
  const byBackend = new Map<string, BackendEndpointStats>();
  for (const status of statuses) {
    const stats = status.previousResource
      ? anyUnpack(status.previousResource, BackendEndpointStatsSchema)
      : undefined;
    if (!stats) {
      continue;
    }
    const merged =
      byBackend.get(stats.backendUuid) ??
      create(BackendEndpointStatsSchema, { backendUuid: stats.backendUuid });
    merged.endpointStats.push(...stats.endpointStats);
    byBackend.set(stats.backendUuid, merged);
  }
  return byBackend;
}

export function vsHealth(vs: VirtualServer, stats: Map<string, BackendEndpointStats>): Health {
  const endpoints = vsBackendUuids(vs).flatMap((uuid) => stats.get(uuid)?.endpointStats ?? []);
  return {
    healthy: endpoints.filter((endpoint) => endpoint.healthy).length,
    total: endpoints.length,
  };
}

// isDegraded is a virtual server with endpoints of which some are down, one without endpoint is not judged
export function isDegraded(health: Health): boolean {
  return health.total > 0 && health.healthy < health.total;
}

// InstanceHealth holds what the agents report on the selected instance and refreshes it while the tab is
// shown, so every page and the menu badge show the same state with one set of calls
@Injectable({ providedIn: 'root' })
export class InstanceHealth {
  private api = inject(Api);
  private instances = inject(Instances);
  private document = inject(DOCUMENT);

  readonly vips = signal<VIP[]>([]);
  readonly vss = signal<VirtualServer[]>([]);
  // null until the first answer, the views tell they are loading
  readonly agentsStatus = signal<GetAgentStatusResponse[] | null>(null);
  readonly nodes = signal<InstanceNode[]>([]);
  readonly endpointStats = signal<Map<string, BackendEndpointStats> | null>(null);
  readonly updatedAt = signal<Date | null>(null);

  readonly agentsReport = computed(() =>
    agentsReport(this.instances.agents(), this.agentsStatus(), this.nodes()),
  );

  readonly degraded = computed(() => {
    const stats = this.endpointStats() ?? new Map();
    return this.vss().filter((vs) => isDegraded(vsHealth(vs, stats)));
  });
  // error when a degraded virtual server has no healthy endpoint left
  readonly degradedLevel = computed<HealthLevel>(() => {
    const stats = this.endpointStats() ?? new Map();
    const anyDown = this.degraded().some((vs) => vsHealth(vs, stats).healthy === 0);
    return anyDown ? 'error' : 'warning';
  });

  constructor() {
    effect(() => {
      const instance = this.instances.current();
      untracked(() => {
        this.clear();
        if (instance) {
          this.refresh(true);
        }
      });
    });
    setInterval(() => {
      if (this.document.visibilityState === 'visible') {
        this.refresh(true);
      }
    }, refreshMs);
    // a tab coming back shows the current state, not the one of when it was hidden
    this.document.addEventListener('visibilitychange', () => {
      if (this.document.visibilityState === 'visible') {
        this.refresh(true);
      }
    });
  }

  health(vs: VirtualServer): Health | null {
    const stats = this.endpointStats();
    return stats ? vsHealth(vs, stats) : null;
  }

  vipsOf(vs: VirtualServer): VIP[] {
    return this.vips().filter((vip) => vip.virtualServersUuid.includes(vs.UUID));
  }

  // refresh reloads the instance, silent ones come from the timer and must not open an error dialog
  async refresh(silent = false): Promise<void> {
    const instance = this.instances.current();
    if (!instance) {
      return;
    }
    const contextValues = createContextValues().set(silentErrors, silent);
    try {
      const [vips, agentsStatus, nodes, statuses] = await Promise.all([
        this.listVips(instance, contextValues),
        // an instance whose agents give no status (or with no agent) still shows its virtual servers
        this.api.lookup
          .listAgentStatus(
            { instanceUuid: instance.UUID, showAnnouncingVips: true },
            { contextValues: createContextValues().set(silentErrors, true) },
          )
          .then((resp) => resp.agentStatus)
          .catch(() => []),
        this.api.lookup
          .listInstanceNodes(
            { instanceUuid: instance.UUID },
            { contextValues: createContextValues().set(silentErrors, true) },
          )
          .then((resp) => resp.nodes)
          .catch(() => []),
        this.api.lookup.listStatus(
          { instanceUuid: instance.UUID, typeUrl: typeUrl(kinds.endpoint) },
          { contextValues },
        ),
      ]);
      const vss = await this.listVss(vips, contextValues);
      // the answer of an instance no longer selected
      if (this.instances.current() !== instance) {
        return;
      }
      this.vips.set(vips);
      this.vss.set(vss);
      this.agentsStatus.set(agentsStatus);
      this.nodes.set(nodes);
      this.endpointStats.set(mergeEndpointStats(statuses.status));
      this.updatedAt.set(new Date());
    } catch {
      // the last state stays shown, a manual refresh reports the error in a dialog
    }
  }

  private clear() {
    this.vips.set([]);
    this.vss.set([]);
    this.agentsStatus.set(null);
    this.nodes.set([]);
    this.endpointStats.set(null);
    this.updatedAt.set(null);
  }

  private listVips(instance: Instance, contextValues: ContextValues) {
    const filters = [linkedToFilter(instance)];
    return listAll((limit, offset) =>
      this.api.vip.listVIP({ limit, offset, filters }, { contextValues }).then((resp) => resp.vip),
    );
  }

  private async listVss(vips: VIP[], contextValues: ContextValues) {
    // with no filter the list would give every virtual server, not the ones of this instance
    if (vips.length === 0) {
      return [];
    }
    const filters = vips.map((vip) => linkedToFilter(vip));
    return listAll((limit, offset) =>
      this.api.vs.listVS({ limit, offset, filters }, { contextValues }).then((resp) => resp.vs),
    );
  }
}
