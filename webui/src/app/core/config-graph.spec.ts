import { create } from '@bufbuild/protobuf';
import {
  Backend,
  BackendEndpointStats,
  BackendSchema,
  BackendEndpointStatsSchema,
} from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import {
  VirtualServer,
  VirtualServerSchema,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import {
  buildConfigGraph,
  ConfigGraph,
  endpointAddress,
  pathsThrough,
  recap,
} from './config-graph';

function vip(uuid: string, ip = '203.0.113.1') {
  return create(VIPSchema, { UUID: uuid, name: `vip ${uuid}`, ip });
}

function backend(uuid: string) {
  return create(BackendSchema, { UUID: uuid, name: `backend ${uuid}` });
}

function httpVs(routes: { prefix: string; action: unknown }[]) {
  return create(VirtualServerSchema, {
    UUID: 's1',
    name: 'shop',
    port: 443,
    routeConfig: {
      case: 'httpRoutesConfig',
      value: {
        virtualHosts: [
          {
            domains: ['shop.example.com'],
            routes: routes.map((route) => ({
              match: { pathSpecifier: { case: 'prefix', value: route.prefix } },
              action: route.action,
            })),
          },
        ],
      },
    },
  } as never);
}

function toBackend(uuid: string) {
  return { case: 'backendUuid', value: uuid };
}

function stats(uuid: string, endpoints: [string, number, boolean, string?][]) {
  return create(BackendEndpointStatsSchema, {
    backendUuid: uuid,
    endpointStats: endpoints.map(([ip, port, healthy, lastError]) => ({
      ipAddr: { ip, port },
      healthy,
      lastError,
    })),
  });
}

function statsMap(...all: BackendEndpointStats[]) {
  return new Map(all.map((s) => [s.backendUuid, s]));
}

function backendMap(...uuids: string[]) {
  return new Map<string, Backend | null>(uuids.map((uuid) => [uuid, backend(uuid)]));
}

function ids(graph: ConfigGraph) {
  return graph.nodes.map((node) => node.id);
}

function edgeIds(graph: ConfigGraph) {
  return graph.edges.map((edge) => edge.id);
}

function node(graph: ConfigGraph, id: string) {
  const found = graph.nodes.find((n) => n.id === id);
  if (!found) {
    throw new Error(`no node ${id} in ${ids(graph)}`);
  }
  return found;
}

function twoRouteGraph() {
  return buildConfigGraph({
    vs: httpVs([
      { prefix: '/', action: toBackend('c1') },
      { prefix: '/api', action: toBackend('c2') },
    ]),
    vips: [vip('v1')],
    backends: backendMap('c1', 'c2'),
    endpointStats: statsMap(stats('c1', [['10.1.0.4', 8080, true]])),
  });
}

describe('config-graph', () => {
  it('links the vips, the routes, the backends and the endpoints', () => {
    const graph = twoRouteGraph();

    expect(ids(graph).sort()).toEqual(
      [
        'vip:v1',
        'vs:s1',
        'route:0',
        'route:1',
        'backend:c1',
        'backend:c2',
        'endpoint:10.1.0.4:8080',
      ].sort(),
    );
    expect(edgeIds(graph).sort()).toEqual(
      [
        'vip:v1->vs:s1',
        'vs:s1->route:0',
        'vs:s1->route:1',
        'route:0->backend:c1',
        'route:1->backend:c2',
        'backend:c1->endpoint:10.1.0.4:8080',
      ].sort(),
    );
    expect(node(graph, 'route:1').subtitle).toBe('Prefix: /api');
    expect(node(graph, 'route:1').title).toBe('shop.example.com');
    expect(node(graph, 'route:1').testRoute).toMatchObject({ case: 'http' });
    expect(node(graph, 'vs:s1').title).toBe('shop :443');
  });

  it('draws a shared resource once', () => {
    const graph = buildConfigGraph({
      vs: httpVs([
        { prefix: '/', action: toBackend('c1') },
        { prefix: '/api', action: toBackend('c1') },
        { prefix: '/static', action: toBackend('c2') },
      ]),
      vips: [vip('v1')],
      backends: backendMap('c1', 'c2'),
      endpointStats: statsMap(
        stats('c1', [['10.0.0.1', 80, true]]),
        stats('c2', [['10.0.0.1', 80, true]]),
      ),
    });

    expect(graph.nodes.filter((n) => n.id === 'backend:c1').length).toBe(1);
    expect(graph.edges.filter((e) => e.target === 'backend:c1').length).toBe(2);
    const endpoints = graph.nodes.filter((n) => n.id === 'endpoint:10.0.0.1:80');
    expect(endpoints.length).toBe(1);
    expect(endpoints[0].reports?.length).toBe(2);
    expect(graph.edges.filter((e) => e.target === 'endpoint:10.0.0.1:80').length).toBe(2);
    expect(new Set(graph.edges.map((e) => e.id)).size).toBe(graph.edges.length);
  });

  it('colors backends from the raw reports and endpoints from any report', () => {
    const graph = buildConfigGraph({
      vs: httpVs([{ prefix: '/', action: toBackend('c1') }]),
      vips: [vip('v1')],
      backends: backendMap('c1'),
      endpointStats: statsMap(
        stats('c1', [
          ['10.0.0.1', 80, true],
          ['10.0.0.1', 80, false, 'connection refused'],
        ]),
      ),
    });

    expect(node(graph, 'backend:c1').health).toBe('warning');
    expect(node(graph, 'backend:c1').subtitle).toBe('1 of 2 up');
    const endpoint = node(graph, 'endpoint:10.0.0.1:80');
    expect(endpoint.health).toBe('error');
    expect(endpoint.subtitle).toBe('down · connection refused');
    expect(graph.edges.find((e) => e.target === endpoint.id)?.failed).toBe(true);
  });

  it('marks a missing backend', () => {
    const graph = buildConfigGraph({
      vs: httpVs([{ prefix: '/', action: toBackend('c9') }]),
      vips: [vip('v1')],
      backends: new Map([['c9', null]]),
      endpointStats: new Map(),
    });

    const missing = node(graph, 'backend:c9');
    expect(missing.title).toBe('missing backend');
    expect(missing.subtitle).toBe('c9');
    expect(missing.health).toBe('error');
    expect(graph.edges.find((e) => e.target === 'backend:c9')?.failed).toBe(true);
  });

  it('leaves backends without reports unknown', () => {
    const graph = buildConfigGraph({
      vs: httpVs([{ prefix: '/', action: toBackend('c1') }]),
      vips: [vip('v1')],
      backends: backendMap('c1'),
      endpointStats: new Map(),
    });

    expect(node(graph, 'backend:c1').health).toBe('unknown');
    expect(node(graph, 'backend:c1').subtitle).toBe('no endpoint reported');
    expect(node(graph, 'vs:s1').health).toBe('unknown');
  });

  it('routes tls, tcp and udp virtual servers', () => {
    const tls = create(VirtualServerSchema, {
      UUID: 's2',
      name: 'secure',
      port: 443,
      routeConfig: {
        case: 'tlsTcpRoutesConfig',
        value: {
          routes: [
            {
              backendUuid: 'c1',
              mode: { case: 'tls', value: { serverNames: ['a.test'] } },
            },
            {
              backendUuid: 'c2',
              mode: { case: 'passthrough', value: {} },
            },
          ],
        },
      },
    } as never);
    const tlsGraph = buildConfigGraph({
      vs: tls,
      vips: [vip('v1')],
      backends: backendMap('c1', 'c2'),
      endpointStats: new Map(),
    });
    expect(node(tlsGraph, 'route:0').title).toBe('a.test');
    expect(node(tlsGraph, 'route:0').testRoute).toMatchObject({ case: 'tls' });
    expect(node(tlsGraph, 'route:0').subtitle).toBe('terminated');
    expect(node(tlsGraph, 'route:1').title).toBe('any SNI');
    expect(node(tlsGraph, 'route:1').anyHost).toBe(true);
    expect(node(tlsGraph, 'route:0').anyHost).toBeFalsy();
    expect(node(tlsGraph, 'route:1').subtitle).toBe('passthrough');

    const udp = create(VirtualServerSchema, {
      UUID: 's3',
      name: 'dns',
      port: 53,
      routeConfig: { case: 'udpRouteConfig', value: { backendUuid: 'c1' } },
    } as never);
    const udpGraph = buildConfigGraph({
      vs: udp,
      vips: [vip('v1')],
      backends: backendMap('c1'),
      endpointStats: new Map(),
    });
    expect(node(udpGraph, 'route:0').title).toBe('UDP route');
    expect(node(udpGraph, 'route:0').subtitle).toBe('whole port');
    expect(node(udpGraph, 'route:0').dashed).toBe(true);

    const redirectGraph = buildConfigGraph({
      vs: httpVs([
        { prefix: '/', action: { case: 'redirect', value: { hostRedirect: 'x.test' } } },
      ]),
      vips: [vip('v1')],
      backends: new Map(),
      endpointStats: new Map(),
    });
    expect(node(redirectGraph, 'route:0').dashed).toBe(true);
    expect(node(redirectGraph, 'route:0').subtitle).toContain(' · ');
    expect(redirectGraph.edges.some((e) => e.source === 'route:0')).toBe(false);
  });

  it('names a route for any host instead of showing *', () => {
    const vs = httpVs([{ prefix: '/', action: toBackend('c1') }]);
    if (vs.routeConfig.case === 'httpRoutesConfig') {
      vs.routeConfig.value.virtualHosts[0].domains = ['*'];
    }
    const graph = buildConfigGraph({
      vs,
      vips: [vip('v1')],
      backends: backendMap('c1'),
      endpointStats: new Map(),
    });

    expect(node(graph, 'route:0').title).toBe('any domain');
    expect(node(graph, 'route:0').anyHost).toBe(true);
  });

  it('ignores empty backend uuids', () => {
    const graph = buildConfigGraph({
      vs: httpVs([{ prefix: '/', action: toBackend('') }]),
      vips: [vip('v1')],
      backends: new Map(),
      endpointStats: new Map(),
    });

    expect(graph.nodes.some((n) => n.kind === 'backend')).toBe(false);
    expect(graph.edges.some((e) => e.source === 'route:0')).toBe(false);
  });

  it('formats ipv6 endpoint addresses', () => {
    expect(endpointAddress('fd00::1', 80)).toBe('[fd00::1]:80');
    expect(endpointAddress('10.0.0.1', 80)).toBe('10.0.0.1:80');
  });

  function graphWithTwoEndpointsOnC1(sharedWithC2: boolean) {
    return buildConfigGraph({
      vs: httpVs([
        { prefix: '/', action: toBackend('c1') },
        { prefix: '/api', action: toBackend('c2') },
      ]),
      vips: [vip('v1')],
      backends: backendMap('c1', 'c2'),
      endpointStats: statsMap(
        stats('c1', [
          ['10.1.0.4', 8080, true],
          ['10.1.0.5', 8080, true],
        ]),
        stats('c2', [sharedWithC2 ? ['10.1.0.4', 8080, true] : ['10.2.0.1', 80, true]]),
      ),
    });
  }

  it('lights the paths upstream and downstream of a node', () => {
    const graph = graphWithTwoEndpointsOnC1(false);

    const lit = pathsThrough(graph, 'backend:c1');

    expect([...lit.nodes].sort()).toEqual(
      [
        'vip:v1',
        'vs:s1',
        'route:0',
        'backend:c1',
        'endpoint:10.1.0.4:8080',
        'endpoint:10.1.0.5:8080',
      ].sort(),
    );
    expect([...lit.edges].sort()).toEqual(
      [
        'vip:v1->vs:s1',
        'vs:s1->route:0',
        'route:0->backend:c1',
        'backend:c1->endpoint:10.1.0.4:8080',
        'backend:c1->endpoint:10.1.0.5:8080',
      ].sort(),
    );
  });

  it('does not climb back up from a shared endpoint', () => {
    const graph = graphWithTwoEndpointsOnC1(true);

    const lit = pathsThrough(graph, 'backend:c1');

    expect(lit.nodes.has('endpoint:10.1.0.4:8080')).toBe(true);
    expect(lit.nodes.has('backend:c2')).toBe(false);
  });

  it('recaps the virtual server', () => {
    const vs = httpVs([
      { prefix: '/', action: toBackend('c1') },
      { prefix: '/api', action: toBackend('c2') },
    ]);
    const graph = buildConfigGraph({
      vs,
      vips: [vip('v1'), vip('v2', '203.0.113.2')],
      backends: backendMap('c1', 'c2'),
      endpointStats: statsMap(
        stats('c1', [['10.1.0.4', 8080, true]]),
        stats('c2', [['10.1.0.5', 8080, false]]),
      ),
    });
    expect(recap(graph)).toBe('HTTP · 2 VIPs · 2 routes · 2 backends · 1 of 2 endpoints up');

    const single: VirtualServer = httpVs([{ prefix: '/', action: toBackend('c1') }]);
    const singleGraph = buildConfigGraph({
      vs: single,
      vips: [vip('v1')],
      backends: backendMap('c1'),
      endpointStats: new Map(),
    });
    expect(recap(singleGraph)).toBe('HTTP · 1 VIP · 1 route · 1 backend');
  });
});
