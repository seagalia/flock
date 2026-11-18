import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { anyPack } from '@bufbuild/protobuf/wkt';
import {
  BackendEndpointStatsSchema,
  EndpointStat,
  EndpointStatSchema,
} from '../../gen/flock/api/config/backend/v1/backend_pb';
import { InstanceSchema } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import {
  VirtualServer,
  VirtualServerSchema,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { StatusResponseSchema } from '../../gen/flock/services/lookup/v1/lookup_pb';
import { Api } from './api';
import { InstanceHealth, isDegraded, mergeEndpointStats, vsHealth } from './instance-health';
import { Instances } from './instances';

const endpoint = (port: number, healthy: boolean, lastError?: string): EndpointStat =>
  create(EndpointStatSchema, { ipAddr: { ip: '10.0.0.1', port }, healthy, lastError });

const statusOf = (backendUuid: string, ...endpoints: EndpointStat[]) =>
  create(StatusResponseSchema, {
    previousResource: anyPack(
      BackendEndpointStatsSchema,
      create(BackendEndpointStatsSchema, { backendUuid, endpointStats: endpoints }),
    ),
  });

const tcpVs = (uuid: string, backendUuid: string): VirtualServer =>
  create(VirtualServerSchema, {
    UUID: uuid,
    name: uuid,
    routeConfig: { case: 'tcpRouteConfig', value: { backendUuid } },
  });

describe('instance health', () => {
  it('merges the endpoints every agent reports for a backend', () => {
    const merged = mergeEndpointStats([
      statusOf('c1', endpoint(80, true)),
      statusOf('c1', endpoint(81, false, 'connection refused')),
      statusOf('c2', endpoint(90, true)),
      create(StatusResponseSchema, {}),
    ]);

    expect(merged.get('c1')?.endpointStats.map((stat) => stat.ipAddr?.port)).toEqual([80, 81]);
    expect(merged.get('c2')?.endpointStats.length).toBe(1);
  });

  it('counts the healthy endpoints of a virtual server', () => {
    const merged = mergeEndpointStats([statusOf('c1', endpoint(80, true), endpoint(81, false))]);

    const health = vsHealth(tcpVs('web', 'c1'), merged);

    expect(health).toEqual({ healthy: 1, total: 2 });
    expect(isDegraded(health)).toBe(true);
    expect(isDegraded({ healthy: 2, total: 2 })).toBe(false);
    expect(isDegraded(vsHealth(tcpVs('none', 'unknown'), merged))).toBe(false);
  });

  describe('InstanceHealth', () => {
    const instance = create(InstanceSchema, { UUID: 'inst-1', name: 'local' });
    const vip = create(VIPSchema, { UUID: 'vip-1', ip: '10.0.0.10', virtualServersUuid: ['web'] });
    let statuses = [statusOf('c-web', endpoint(80, true), endpoint(81, false))];
    let failing = false;
    let agentStatusFailing = false;

    const api = {
      instance: { listInstance: async () => ({ instances: [instance] }) },
      vip: { listVIP: async () => ({ vip: [vip] }) },
      vs: {
        listVS: async () => ({ vs: [tcpVs('web', 'c-web'), tcpVs('db', 'c-db')] }),
      },
      lookup: {
        listAgent: async () => ({ agents: [] }),
        listAgentStatus: async () => {
          if (agentStatusFailing) {
            throw new Error('no agent with the status feature');
          }
          return { agentStatus: [] };
        },
        listInstanceNodes: async () => ({ nodes: [] }),
        listStatus: async () => {
          if (failing) {
            throw new Error('controller unreachable');
          }
          return { status: statuses };
        },
      },
    };

    beforeEach(() => {
      statuses = [statusOf('c-web', endpoint(80, true), endpoint(81, false))];
      failing = false;
      agentStatusFailing = false;
      TestBed.configureTestingModule({
        providers: [provideRouter([]), { provide: Api, useValue: api }],
      });
    });

    const loaded = async () => {
      const health = TestBed.inject(InstanceHealth);
      await TestBed.inject(Instances).load();
      await health.refresh();
      return health;
    };

    it('loads the virtual servers of the instance and lists the degraded ones', async () => {
      const health = await loaded();

      expect(health.vss().map((vs) => vs.UUID)).toEqual(['web', 'db']);
      expect(health.degraded().map((vs) => vs.UUID)).toEqual(['web']);
      expect(health.degradedLevel()).toBe('warning');
      expect(health.vipsOf(tcpVs('web', 'c-web'))).toEqual([vip]);
      expect(health.updatedAt()).not.toBeNull();
    });

    it('is an error level when a degraded virtual server has no healthy endpoint', async () => {
      statuses = [statusOf('c-web', endpoint(80, false))];

      const health = await loaded();

      expect(health.degradedLevel()).toBe('error');
    });

    it('still shows the virtual servers when the agents give no status', async () => {
      agentStatusFailing = true;

      const health = await loaded();

      expect(health.vss().map((vs) => vs.UUID)).toEqual(['web', 'db']);
      expect(health.agentsStatus()).toEqual([]);
    });

    it('keeps the last state when a refresh fails', async () => {
      const health = await loaded();
      failing = true;

      await health.refresh(true);

      expect(health.degraded().map((vs) => vs.UUID)).toEqual(['web']);
    });
  });
});
