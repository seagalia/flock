import { Backend, BackendEndpointStats } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { Route } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { HealthLevel, healthLevel, routeActionLabel, routeMatchLabel, vsType } from './display';
import { vsHealth } from './instance-health';
import { TestRoute } from './test-commands';

export type NodeKind = 'vip' | 'vs' | 'route' | 'backend' | 'endpoint';

export const columnOf: Record<NodeKind, number> = {
  vip: 0,
  vs: 1,
  route: 2,
  backend: 3,
  endpoint: 4,
};

export interface EndpointReport {
  backendUuid: string;
  backendName: string;
  healthy: boolean;
  lastError?: string;
}

export interface GraphNode {
  id: string;
  kind: NodeKind;
  title: string;
  subtitle: string;
  // null: not judged (vips, routes), no status dot
  health: HealthLevel | null;
  // the resource opened by "Open details": the vip, the vs (also for its routes), the backend
  resource?: VIP | VirtualServer | Backend;
  route?: Route; // http routes only
  testRoute?: TestRoute; // http and tls routes, the whole port of tcp/udp is the virtual server's
  reports?: EndpointReport[]; // endpoints only
  missing?: boolean; // backend referenced but not found
  dashed?: boolean; // tcp/udp "whole port" routes, redirect and direct-response routes
  anyHost?: boolean; // route title says "any domain" / "any SNI" rather than naming hosts
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  failed: boolean;
}

export interface ConfigGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

export interface ConfigGraphInput {
  vs: VirtualServer;
  vips: VIP[];
  // null value: the backend is referenced but was not found
  backends: Map<string, Backend | null>;
  endpointStats: Map<string, BackendEndpointStats>;
}

export function endpointAddress(ip: string, port: number): string {
  const isIpv6 = ip.includes(':');
  return isIpv6 ? `[${ip}]:${port}` : `${ip}:${port}`;
}

// buildConfigGraph links vips → virtual server → routes → backends → endpoints. Nodes and edges are kept
// by id so a backend or an endpoint used by several parents is drawn once.
export function buildConfigGraph(input: ConfigGraphInput): ConfigGraph {
  const { vs, vips, backends, endpointStats } = input;
  const nodes = new Map<string, GraphNode>();
  const edges = new Map<string, GraphEdge>();

  function link(source: string, target: string) {
    const id = `${source}->${target}`;
    edges.set(id, { id, source, target, failed: false });
  }

  function addBackend(routeId: string, uuid: string) {
    if (uuid === '') {
      return;
    }
    const backendId = `backend:${uuid}`;
    link(routeId, backendId);
    if (nodes.has(backendId)) {
      return;
    }

    const backend = backends.get(uuid);
    if (!backend) {
      nodes.set(backendId, {
        id: backendId,
        kind: 'backend',
        title: 'missing backend',
        subtitle: uuid,
        health: 'error',
        missing: true,
      });
      return;
    }

    // the raw reports of every agent are counted, an address seen by two agents counts twice
    const reports = endpointStats.get(uuid)?.endpointStats ?? [];
    const healthy = reports.filter((report) => report.healthy).length;
    nodes.set(backendId, {
      id: backendId,
      kind: 'backend',
      title: backend.name,
      subtitle: reports.length > 0 ? `${healthy} of ${reports.length} up` : 'no endpoint reported',
      health: reports.length > 0 ? healthLevel(healthy, reports.length) : 'unknown',
      resource: backend,
    });

    for (const report of reports) {
      const address = endpointAddress(report.ipAddr?.ip ?? '', report.ipAddr?.port ?? 0);
      const endpointId = `endpoint:${address}`;
      link(backendId, endpointId);
      const endpointReport: EndpointReport = {
        backendUuid: uuid,
        backendName: backend.name,
        healthy: report.healthy,
        lastError: report.lastError,
      };
      const known = nodes.get(endpointId);
      if (known) {
        known.reports?.push(endpointReport);
        continue;
      }
      nodes.set(endpointId, {
        id: endpointId,
        kind: 'endpoint',
        title: address,
        subtitle: '',
        health: 'ok',
        reports: [endpointReport],
      });
    }
  }

  for (const vip of vips) {
    nodes.set(`vip:${vip.UUID}`, {
      id: `vip:${vip.UUID}`,
      kind: 'vip',
      title: vip.ip,
      subtitle: vip.name,
      health: null,
      resource: vip,
    });
    link(`vip:${vip.UUID}`, `vs:${vs.UUID}`);
  }

  const health = vsHealth(vs, endpointStats);
  nodes.set(`vs:${vs.UUID}`, {
    id: `vs:${vs.UUID}`,
    kind: 'vs',
    title: `${vs.name} :${vs.port}`,
    subtitle: vsType(vs),
    health: health.total > 0 ? healthLevel(health.healthy, health.total) : 'unknown',
    resource: vs,
  });

  let routeCount = 0;
  function addRoute(node: Omit<GraphNode, 'id' | 'kind' | 'health'>, backendUuid: string) {
    const id = `route:${routeCount}`;
    routeCount++;
    nodes.set(id, { id, kind: 'route', health: null, resource: vs, ...node });
    link(`vs:${vs.UUID}`, id);
    addBackend(id, backendUuid);
  }

  const config = vs.routeConfig;
  if (config.case === 'httpRoutesConfig') {
    for (const vhost of config.value.virtualHosts) {
      const anyDomain = vhost.domains.length === 0 || vhost.domains.includes('*');
      for (const route of vhost.routes) {
        const toBackend = route.action.case === 'backendUuid';
        const action = toBackend ? '' : ` · ${routeActionLabel(route)}`;
        addRoute(
          {
            title: anyDomain ? 'any domain' : vhost.domains.join(', '),
            subtitle: routeMatchLabel(route) + action,
            anyHost: anyDomain,
            route,
            testRoute: { case: 'http', vhost, route },
            dashed: !toBackend,
          },
          route.action.case === 'backendUuid' ? route.action.value : '',
        );
      }
    }
  }
  if (config.case === 'tlsTcpRoutesConfig') {
    for (const tls of config.value.routes) {
      // the server names live in the mode message, whichever the mode
      const serverNames = tls.mode.case ? tls.mode.value.serverNames : [];
      const anySni = serverNames.length === 0;
      addRoute(
        {
          title: anySni ? 'any SNI' : serverNames.join(', '),
          anyHost: anySni,
          testRoute: { case: 'tls', route: tls },
          subtitle: tls.mode.case === 'passthrough' ? 'passthrough' : 'terminated',
        },
        tls.backendUuid,
      );
    }
  }
  if (config.case === 'tcpRouteConfig' || config.case === 'udpRouteConfig') {
    const protocol = config.case === 'tcpRouteConfig' ? 'TCP' : 'UDP';
    addRoute(
      { title: `${protocol} route`, subtitle: 'whole port', dashed: true },
      config.value.backendUuid,
    );
  }

  for (const node of nodes.values()) {
    if (node.kind !== 'endpoint') {
      continue;
    }
    const reports = node.reports ?? [];
    const down = reports.find((report) => !report.healthy);
    node.health = down ? 'error' : 'ok';
    const lastError = reports.find((report) => report.lastError)?.lastError;
    if (!down) {
      node.subtitle = 'healthy';
      continue;
    }
    node.subtitle = lastError ? `down · ${lastError}` : 'down';
  }

  for (const edge of edges.values()) {
    edge.failed = nodes.get(edge.target)?.health === 'error';
  }
  return { nodes: [...nodes.values()], edges: [...edges.values()] };
}

export interface Highlight {
  nodes: Set<string>;
  edges: Set<string>;
}

// pathsThrough gives the node, everything upstream of it and everything downstream of it. The walks only go
// one way each, so a shared endpoint never leads back up to another backend.
export function pathsThrough(graph: ConfigGraph, nodeId: string): Highlight {
  const lit: Highlight = { nodes: new Set([nodeId]), edges: new Set() };

  function walk(from: 'source' | 'target') {
    const next = from === 'source' ? 'target' : 'source';
    const pending = [nodeId];
    const visited = new Set(pending);
    while (pending.length > 0) {
      const current = pending.pop() as string;
      for (const edge of graph.edges) {
        if (edge[from] !== current) {
          continue;
        }
        lit.edges.add(edge.id);
        lit.nodes.add(edge[next]);
        if (!visited.has(edge[next])) {
          visited.add(edge[next]);
          pending.push(edge[next]);
        }
      }
    }
  }

  walk('source');
  walk('target');
  return lit;
}

function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? '' : 's'}`;
}

// recap is "HTTP · 1 VIP · 3 routes · 2 backends · 3 of 4 endpoints up"
export function recap(graph: ConfigGraph): string {
  const ofKind = (kind: NodeKind) => graph.nodes.filter((node) => node.kind === kind);
  const endpoints = ofKind('endpoint');
  const up = endpoints.filter((endpoint) => endpoint.health === 'ok').length;
  const parts = [
    ofKind('vs')[0]?.subtitle ?? '',
    plural(ofKind('vip').length, 'VIP'),
    plural(ofKind('route').length, 'route'),
    plural(ofKind('backend').length, 'backend'),
  ];
  if (endpoints.length > 0) {
    parts.push(`${up} of ${plural(endpoints.length, 'endpoint')} up`);
  }
  return parts.join(' · ');
}
