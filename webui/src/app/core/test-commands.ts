import { Route, VirtualHost } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import {
  TlsTcpRouteConfig,
  VirtualServer,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';

// TestCommand is a command line sending traffic to a virtual server through one of its vips
export interface TestCommand {
  label: string;
  command: string;
}

// TestRoute is one route of a virtual server, to send traffic to it alone
export type TestRoute =
  | { case: 'http'; vhost: VirtualHost; route: Route }
  | { case: 'tls'; route: TlsTcpRouteConfig };

// vipAddress is what a client connects to, ipv6 between brackets
export function vipAddress(vip: VIP, vs: VirtualServer): string {
  return `${bracketIpv6(vip.ip)}:${vs.port}`;
}

// testCommands gives a command per host or server name the virtual server, or only one of its routes, routes on
export function testCommands(vip: VIP, vs: VirtualServer, route?: TestRoute): TestCommand[] {
  if (route?.case === 'http') {
    return curlCommands(vip, vs, route.vhost, routePath(route.vhost, route.route));
  }
  if (route?.case === 'tls') {
    return opensslCommands(vipAddress(vip, vs), route.route.mode.value?.serverNames ?? []);
  }
  const config = vs.routeConfig;
  switch (config.case) {
    case 'httpRoutesConfig':
      return config.value.virtualHosts.flatMap((vhost) =>
        curlCommands(vip, vs, vhost, vhost.PathPrefix || '/'),
      );
    case 'tlsTcpRoutesConfig':
      return opensslCommands(
        vipAddress(vip, vs),
        config.value.routes.flatMap((route) => route.mode.value?.serverNames ?? []),
      );
    case 'tcpRouteConfig':
      return [{ label: 'nc · TCP', command: `nc ${vip.ip} ${vs.port}` }];
    case 'udpRouteConfig':
      return [{ label: 'nc · UDP', command: `nc -u ${vip.ip} ${vs.port}` }];
    default:
      return [];
  }
}

function curlCommands(
  vip: VIP,
  vs: VirtualServer,
  vhost: VirtualHost,
  path: string,
): TestCommand[] {
  const isTls = vhost.tlsOptions.length > 0;
  const scheme = isTls ? 'https' : 'http';
  // the certificate is rarely trusted by the machine testing, what is tested is the load balancing
  const curl = isTls ? 'curl -k' : 'curl';
  const hosts = [...new Set(vhost.domains.map(testHost))];
  return hosts.map((host) => {
    if (host === '') {
      return {
        label: `curl · ${scheme} · any host`,
        command: `${curl} ${scheme}://${vipAddress(vip, vs)}${path}`,
      };
    }
    // --resolve connects to the vip while the host stays the Host header and the tls server name
    return {
      label: `curl · ${scheme} · ${host}`,
      command: `${curl} --resolve ${host}:${vs.port}:${bracketIpv6(vip.ip)} ${scheme}://${host}:${vs.port}${path}`,
    };
  });
}

// routePath is a path the route matches: the virtual host's prefix is prepended to its prefix or path. A regex
// gives no path to write, the virtual host's prefix stands.
function routePath(vhost: VirtualHost, route: Route): string {
  const prefix = vhost.PathPrefix;
  const specifier = route.match?.pathSpecifier;
  const isWritten = specifier?.case === 'prefix' || specifier?.case === 'path';
  if (!isWritten || specifier.value === '/') {
    return prefix || '/';
  }
  return prefix.replace(/\/$/, '') + specifier.value;
}

function opensslCommands(address: string, serverNames: string[]): TestCommand[] {
  const names = [...new Set(serverNames.map(testHost))].filter((name) => name !== '');
  if (names.length === 0) {
    return [{ label: 'openssl · no SNI', command: `openssl s_client -connect ${address}` }];
  }
  return names.map((name) => ({
    label: `openssl · ${name}`,
    command: `openssl s_client -connect ${address} -servername ${name}`,
  }));
}

// testHost gives a name the domain matches, '' for a domain matching any host
function testHost(domain: string): string {
  const host = domain.startsWith('[') ? domain : domain.split(':')[0];
  if (host === '*') {
    return '';
  }
  return host.replace('*', 'test');
}

function bracketIpv6(ip: string): string {
  return ip.includes(':') ? `[${ip}]` : ip;
}
