import { create, MessageInitShape } from '@bufbuild/protobuf';
import { TLSSchema } from '../../gen/flock/api/config/core/v1/tls_pb';
import { VirtualHostSchema } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import {
  HttpRoutesConfigSchema,
  TlsPassthroughSchema,
  TlsTcpRouteConfigSchema,
  TlsTcpRoutesConfigSchema,
  VirtualServerSchema,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { testCommands, vipAddress } from './test-commands';

const tls = create(TLSSchema, { certificatesUuid: ['cert'] });

function httpVs(...virtualHosts: MessageInitShape<typeof VirtualHostSchema>[]) {
  return create(VirtualServerSchema, {
    port: 8443,
    routeConfig: {
      case: 'httpRoutesConfig',
      value: create(HttpRoutesConfigSchema, {
        virtualHosts: virtualHosts.map((vhost) => create(VirtualHostSchema, vhost)),
      }),
    },
  });
}

function commandsOf(vs: ReturnType<typeof httpVs>, ip = '10.0.0.1'): string[] {
  return testCommands(create(VIPSchema, { ip }), vs).map((one) => one.command);
}

describe('test commands', () => {
  it('gives the address a client connects to, ipv6 between brackets', () => {
    const vs = create(VirtualServerSchema, { port: 443 });

    expect(vipAddress(create(VIPSchema, { ip: '10.0.0.1' }), vs)).toBe('10.0.0.1:443');
    expect(vipAddress(create(VIPSchema, { ip: '2001:db8::1' }), vs)).toBe('[2001:db8::1]:443');
  });

  it('curls each domain of an http virtual server through the vip', () => {
    const vs = httpVs({ domains: ['a.example.com', 'a.example.com:*'] }, { domains: ['*'] });

    expect(commandsOf(vs)).toEqual([
      'curl --resolve a.example.com:8443:10.0.0.1 http://a.example.com:8443/',
      'curl http://10.0.0.1:8443/',
    ]);
  });

  it('curls in https a virtual host with tls, on its path prefix', () => {
    const vs = httpVs({ domains: ['*.example.com'], tlsOptions: [tls], PathPrefix: '/app' });

    expect(commandsOf(vs)).toEqual([
      'curl -k --resolve test.example.com:8443:10.0.0.1 https://test.example.com:8443/app',
    ]);
  });

  it('puts an ipv6 vip between brackets', () => {
    const vs = httpVs({ domains: ['a.example.com'] }, { domains: ['*'] });

    expect(commandsOf(vs, '2001:db8::1')).toEqual([
      'curl --resolve a.example.com:8443:[2001:db8::1] http://a.example.com:8443/',
      'curl http://[2001:db8::1]:8443/',
    ]);
  });

  it('opens a tls connection per server name of a tls over tcp virtual server', () => {
    const terminated = create(TlsTcpRouteConfigSchema, {
      mode: { case: 'tls', value: create(TLSSchema, { serverNames: ['a.local'] }) },
    });
    const passthrough = create(TlsTcpRouteConfigSchema, {
      mode: {
        case: 'passthrough',
        value: create(TlsPassthroughSchema, { serverNames: ['b.local'] }),
      },
    });
    const vs = create(VirtualServerSchema, {
      port: 9000,
      routeConfig: {
        case: 'tlsTcpRoutesConfig',
        value: create(TlsTcpRoutesConfigSchema, {
          routes: [terminated, passthrough],
        }),
      },
    });

    expect(commandsOf(vs)).toEqual([
      'openssl s_client -connect 10.0.0.1:9000 -servername a.local',
      'openssl s_client -connect 10.0.0.1:9000 -servername b.local',
    ]);
  });

  it('opens a tls connection without server name when no route has one', () => {
    const vs = create(VirtualServerSchema, {
      port: 9000,
      routeConfig: {
        case: 'tlsTcpRoutesConfig',
        value: create(TlsTcpRoutesConfigSchema, {
          routes: [create(TlsTcpRouteConfigSchema, { mode: { case: 'tls', value: tls } })],
        }),
      },
    });

    expect(commandsOf(vs)).toEqual(['openssl s_client -connect 10.0.0.1:9000']);
  });

  it('curls one route of an http virtual server on its own path', () => {
    const vhost = create(VirtualHostSchema, {
      domains: ['a.example.com'],
      PathPrefix: '/app',
      routes: [
        { match: { pathSpecifier: { case: 'prefix', value: '/api' } } },
        { match: { pathSpecifier: { case: 'safeRegex', value: '^/v[0-9]+' } } },
      ],
    });
    const vs = httpVs(vhost);
    const vip = create(VIPSchema, { ip: '10.0.0.1' });
    const commandOf = (route: (typeof vhost.routes)[number]) =>
      testCommands(vip, vs, { case: 'http', vhost, route }).map((one) => one.command);

    expect(commandOf(vhost.routes[0])).toEqual([
      'curl --resolve a.example.com:8443:10.0.0.1 http://a.example.com:8443/app/api',
    ]);
    expect(commandOf(vhost.routes[1])).toEqual([
      'curl --resolve a.example.com:8443:10.0.0.1 http://a.example.com:8443/app',
    ]);
  });

  it('opens a tls connection for the server names of one route only', () => {
    const route = create(TlsTcpRouteConfigSchema, {
      mode: { case: 'tls', value: create(TLSSchema, { serverNames: ['a.local'] }) },
    });
    const vs = create(VirtualServerSchema, {
      port: 9000,
      routeConfig: {
        case: 'tlsTcpRoutesConfig',
        value: create(TlsTcpRoutesConfigSchema, {
          routes: [
            route,
            create(TlsTcpRouteConfigSchema, {
              mode: { case: 'tls', value: create(TLSSchema, { serverNames: ['b.local'] }) },
            }),
          ],
        }),
      },
    });

    const commands = testCommands(create(VIPSchema, { ip: '10.0.0.1' }), vs, {
      case: 'tls',
      route,
    });

    expect(commands.map((one) => one.command)).toEqual([
      'openssl s_client -connect 10.0.0.1:9000 -servername a.local',
    ]);
  });

  it('connects with nc to tcp and udp virtual servers', () => {
    const tcp = create(VirtualServerSchema, {
      port: 9001,
      routeConfig: { case: 'tcpRouteConfig', value: {} },
    });
    const udp = create(VirtualServerSchema, {
      port: 9002,
      routeConfig: { case: 'udpRouteConfig', value: {} },
    });

    expect(commandsOf(tcp)).toEqual(['nc 10.0.0.1 9001']);
    expect(commandsOf(udp)).toEqual(['nc -u 10.0.0.1 9002']);
  });
});
