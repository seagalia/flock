// Labels shown for the enums and oneofs of the api.
import { Duration } from '@bufbuild/protobuf/wkt';
import { Status } from '../../gen/flock/api/config/agent/v1/status_pb';
import { GetAgentStatusResponse } from '../../gen/flock/services/lookup/v1/lookup_pb';
import { State } from '../../gen/flock/api/config/agent/v1/status_pb';
import {
  Backend,
  Backend_LbPolicy,
  Backend_ProxyProtocolVersion,
  DNS_LookupFamily,
} from '../../gen/flock/api/config/backend/v1/backend_pb';
import {
  HealthCheck,
  HealthCheckPayload,
  RequestMethod,
} from '../../gen/flock/api/config/core/v1/healthcheck_pb';
import { URL as FlockUrl } from '../../gen/flock/api/config/core/v1/url_pb';
import { IpPool, IpPool_Privacy } from '../../gen/flock/api/config/zone/v1/zone_pb';
import { Route } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { CodecClientType } from '../../gen/flock/type/v1/http_pb';

export function vsType(vs: VirtualServer): string {
  switch (vs.routeConfig.case) {
    case 'httpRoutesConfig':
      return 'HTTP';
    case 'tlsTcpRoutesConfig':
      return 'TLS+TCP';
    case 'tcpRouteConfig':
      return 'TCP';
    case 'udpRouteConfig':
      return 'UDP';
    default:
      return 'Unknown';
  }
}

// acceptsProxyProtocol is null for a udp virtual server: it has no such option
export function acceptsProxyProtocol(vs: VirtualServer): boolean | null {
  const config = vs.routeConfig;
  switch (config.case) {
    case 'httpRoutesConfig':
    case 'tlsTcpRoutesConfig':
    case 'tcpRouteConfig':
      return config.value.acceptProxyProtocol;
    default:
      return null;
  }
}

export function vsBackendUuids(vs: VirtualServer): string[] {
  const config = vs.routeConfig;
  let uuids: string[] = [];
  if (config.case === 'tlsTcpRoutesConfig') {
    uuids = config.value.routes.map((route) => route.backendUuid);
  }
  if (config.case === 'httpRoutesConfig') {
    for (const vhost of config.value.virtualHosts) {
      for (const route of vhost.routes) {
        if (route.action.case === 'backendUuid') {
          uuids.push(route.action.value);
        }
      }
    }
    // after the route backends, as the controller lists them, so health, the config map and the chain Delete see them
    uuids.push(...config.value.externalAuths.map((policy) => policy.backendUuid));
  }
  if (config.case === 'tcpRouteConfig' || config.case === 'udpRouteConfig') {
    uuids = [config.value.backendUuid];
  }
  return [...new Set(uuids.filter((uuid) => uuid !== ''))];
}

// the endpoint configuration of a backend tells how its endpoints are found
export function backendType(backend: Backend): string {
  switch (backend.endpoint.case) {
    case 'dns':
      return 'DNS';
    case 'ipAddrGroup':
      return 'Static';
    case 'eds':
      return 'EDS';
    default:
      return 'Unknown';
  }
}

const lbPolicyLabels: Record<Backend_LbPolicy, string> = {
  [Backend_LbPolicy.ROUND_ROBIN]: 'Round Robin',
  [Backend_LbPolicy.WEIGHTED_ROUND_ROBIN]: 'Weighted Round Robin',
  [Backend_LbPolicy.LEAST_REQUEST]: 'Least Request',
  [Backend_LbPolicy.WEIGHTED_LEAST_REQUEST]: 'Weighted Least Request',
  [Backend_LbPolicy.RING_HASH]: 'Ring Hash',
  [Backend_LbPolicy.MAGLEV]: 'Maglev',
};

export function lbPolicyLabel(backend: Backend): string {
  return lbPolicyLabels[backend.lbPolicy] ?? 'Unknown';
}

export function proxyProtocolLabel(backend: Backend): string {
  if (!backend.enableProxyProtocol) {
    return 'Disabled';
  }
  const version = backend.proxyProtocolVersion === Backend_ProxyProtocolVersion.V2 ? 'V2' : 'V1';
  return `Enabled ${version}`;
}

export function healthCheckType(healthCheck: HealthCheck | undefined): string {
  switch (healthCheck?.healthChecker.case) {
    case 'noHealthCheck':
      return 'None';
    case 'httpHealthCheck':
      return 'HTTP';
    case 'tcpHealthCheck':
      return 'TCP';
    case 'grpcHealthCheck':
      return 'GRPC';
    case 'udpHealthCheck':
      return 'UDP';
    case 'pluginHealthCheck':
      return 'Plugin';
    default:
      return 'Unknown';
  }
}

export const lookupFamilyLabels: Record<DNS_LookupFamily, string> = {
  [DNS_LookupFamily.V6_PREFERRED]: 'V6 Preferred',
  [DNS_LookupFamily.V4_PREFERRED]: 'V4 Preferred',
  [DNS_LookupFamily.V4_ONLY]: 'V4 Only',
  [DNS_LookupFamily.V6_ONLY]: 'V6 Only',
  [DNS_LookupFamily.ALL]: 'All',
};

export function httpMethodLabel(method: RequestMethod): string {
  // envoy sends a GET when the method is not set
  if (method === RequestMethod.METHOD_UNSPECIFIED) {
    return 'GET';
  }
  return RequestMethod[method];
}

export const codecLabels: Record<CodecClientType, string> = {
  [CodecClientType.HTTP1]: 'HTTP 1.1',
  [CodecClientType.HTTP2]: 'HTTP 2',
  [CodecClientType.HTTP3]: 'HTTP 3',
};

export function formatUrl(url: FlockUrl | undefined): string {
  if (!url) {
    return '';
  }
  const userInfo = url.userInfo ? url.userInfo.user + '@' : '';
  const port = url.port > 0 ? ':' + url.port : '';
  return `${url.scheme}://${userInfo}${url.host}${port}${url.path}`;
}

export function durationLabel(duration: Duration | undefined): string {
  if (!duration) {
    return 'Not set';
  }
  const seconds = Number(duration.seconds) + duration.nanos / 1e9;
  return `${seconds} Seconds`;
}

export function payloadsToJson(payloads: HealthCheckPayload[]): unknown[] {
  return payloads.map((payload) => {
    if (payload.payload.case === 'binary') {
      return { text: '<binary representation>' };
    }
    return { text: payload.payload.value ?? '' };
  });
}

export function routeMatchLabel(route: Route): string {
  const match = route.match;
  if (!match) {
    return '';
  }
  const caseSensitive = match.caseSensitive ? ' (Case Sensitive)' : '';
  switch (match.pathSpecifier.case) {
    case 'path':
      return `Path: ${match.pathSpecifier.value}${caseSensitive}`;
    case 'prefix':
      return `Prefix: ${match.pathSpecifier.value}${caseSensitive}`;
    case 'safeRegex':
      return `Regex: ${match.pathSpecifier.value}`;
    default:
      return '';
  }
}

export function routeActionLabel(route: Route): string {
  const action = route.action;
  if (action.case === 'directResponse') {
    return `Direct response with Status: ${action.value.status}`;
  }
  if (action.case === 'backendUuid') {
    return 'Route to backend';
  }
  if (action.case !== 'redirect') {
    return 'None';
  }
  const redirect = action.value;
  let scheme = '*://';
  if (
    redirect.schemeRewriteSpecifier.case === 'httpsRedirect' &&
    redirect.schemeRewriteSpecifier.value
  ) {
    scheme = 'https://';
  }
  if (redirect.schemeRewriteSpecifier.case === 'schemeRedirect') {
    scheme = redirect.schemeRewriteSpecifier.value + '://';
  }
  const host = redirect.hostRedirect || '*';
  const port = redirect.portRedirect ? ':' + redirect.portRedirect : '';
  const path = redirect.pathRedirect || '/*';
  return `Redirect to ${scheme}${host}${port}${path}`;
}

// the worst status first: one errored component makes the agent errored
const statusSeverity: Record<Status, number> = {
  [Status.OK]: 0,
  [Status.UNKNOWN]: 1,
  [Status.WARNING]: 2,
  [Status.ERRORED]: 3,
};

export function worstStatus(statuses: Status[]): Status {
  let worst = Status.OK;
  for (const status of statuses) {
    if (statusSeverity[status] > statusSeverity[worst]) {
      worst = status;
    }
  }
  return worst;
}

export function agentComponents(agentStatus: GetAgentStatusResponse): State[] {
  const state = agentStatus.agentStatus?.state;
  if (!state?.case) {
    return [];
  }
  return state.value.componentStates;
}

// agentHealth is the agent status including its components, unknown when it sends no status
export function agentHealth(agentStatus: GetAgentStatusResponse): Status {
  const state = agentStatus.agentStatus?.state;
  if (state?.case !== 'sidecarAgentState') {
    return worstStatus(agentComponents(agentStatus).map((component) => component.status));
  }
  const agentOwnStatus = state.value.agentState?.status ?? Status.UNKNOWN;
  return worstStatus([
    agentOwnStatus,
    ...state.value.componentStates.map((component) => component.status),
  ]);
}

export type HealthLevel = 'ok' | 'warning' | 'error' | 'unknown';

// healthLevel tells the color of a "healthy/total" chip
export function healthLevel(healthy: number, total: number): HealthLevel {
  if (healthy === total) {
    return 'ok';
  }
  if (healthy === 0) {
    return 'error';
  }
  return 'warning';
}

// defaultPool is the pool register-vip uses without a pool: the first public one
export function defaultPool(pools: IpPool[]): IpPool | undefined {
  return pools.find((pool) => pool.privacy === IpPool_Privacy.PUBLIC);
}

// metadataLabels lists the metadata as key=value, sorted by key
export function metadataLabels(metadata: Record<string, string>): string[] {
  return Object.keys(metadata)
    .sort()
    .map((key) => `${key}=${metadata[key]}`);
}
