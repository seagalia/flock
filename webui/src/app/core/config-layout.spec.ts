import { create } from '@bufbuild/protobuf';
import {
  BackendEndpointStatsSchema,
  BackendSchema,
} from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { buildConfigGraph, columnOf } from './config-graph';
import { columnHeads, layoutConfigGraph, structureKey } from './config-layout';

function sketchGraph(options: { secondEndpointHealthy: boolean; extraEndpoint: boolean }) {
  const route = (prefix: string, backendUuid: string) => ({
    match: { pathSpecifier: { case: 'prefix', value: prefix } },
    action: { case: 'backendUuid', value: backendUuid },
  });
  const vs = create(VirtualServerSchema, {
    UUID: 's1',
    name: 'shop',
    port: 443,
    routeConfig: {
      case: 'httpRoutesConfig',
      value: {
        virtualHosts: [
          {
            domains: ['shop.example.com'],
            routes: [route('/', 'c1'), route('/api', 'c2'), route('/admin', 'c2')],
          },
        ],
      },
    },
  } as never);
  const endpoint = (ip: string, healthy: boolean) => ({ ipAddr: { ip, port: 8080 }, healthy });
  const c1Endpoints = [endpoint('10.1.0.4', true), endpoint('10.1.0.5', true)];
  const c2Endpoints = [
    endpoint('10.2.0.4', true),
    endpoint('10.2.0.5', options.secondEndpointHealthy),
  ];
  if (options.extraEndpoint) {
    c2Endpoints.push(endpoint('10.2.0.6', true));
  }
  return buildConfigGraph({
    vs,
    vips: [create(VIPSchema, { UUID: 'v1', name: 'public', ip: '203.0.113.1' })],
    backends: new Map([
      ['c1', create(BackendSchema, { UUID: 'c1', name: 'web' })],
      ['c2', create(BackendSchema, { UUID: 'c2', name: 'api' })],
    ]),
    endpointStats: new Map([
      ['c1', create(BackendEndpointStatsSchema, { backendUuid: 'c1', endpointStats: c1Endpoints })],
      ['c2', create(BackendEndpointStatsSchema, { backendUuid: 'c2', endpointStats: c2Endpoints })],
    ]),
  });
}

describe('config-layout', () => {
  const graph = sketchGraph({ secondEndpointHealthy: true, extraEndpoint: false });

  it('places every node', async () => {
    const positions = await layoutConfigGraph(graph);

    expect([...positions.keys()].sort()).toEqual(graph.nodes.map((node) => node.id).sort());
  });

  it('keeps the five columns in order', async () => {
    const positions = await layoutConfigGraph(graph);
    const xOf = (id: string) => positions.get(id)?.x as number;

    for (const edge of graph.edges) {
      expect(xOf(edge.source)).toBeLessThan(xOf(edge.target));
    }
    for (const node of graph.nodes) {
      const sameColumn = graph.nodes.filter(
        (other) => columnOf[other.kind] === columnOf[node.kind],
      );
      for (const other of sameColumn) {
        expect(xOf(other.id)).toBe(xOf(node.id));
      }
    }
  });

  it('puts a head above each column, on one row', async () => {
    const positions = await layoutConfigGraph(graph);
    const heads = columnHeads(graph, positions);
    const top = Math.min(...[...positions.values()].map((point) => point.y));

    expect([...heads.keys()].sort()).toEqual(['backend', 'endpoint', 'route', 'vip', 'vs']);
    for (const node of graph.nodes) {
      expect(heads.get(node.kind)?.x).toBe(positions.get(node.id)?.x);
    }
    const rows = new Set([...heads.values()].map((point) => point.y));
    expect(rows.size).toBe(1);
    expect([...rows][0]).toBeLessThan(top);
  });

  it('keeps positions when only health changes', () => {
    const degraded = sketchGraph({ secondEndpointHealthy: false, extraEndpoint: false });
    const grown = sketchGraph({ secondEndpointHealthy: true, extraEndpoint: true });

    expect(structureKey(degraded)).toBe(structureKey(graph));
    expect(structureKey(grown)).not.toBe(structureKey(graph));
  });
});
