import { create } from '@bufbuild/protobuf';
import { Status } from '../../gen/flock/api/config/agent/v1/status_pb';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { IpPoolSchema, IpPool_Privacy } from '../../gen/flock/api/config/zone/v1/zone_pb';
import { RouteSchema } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import {
  acceptsProxyProtocol,
  backendType,
  defaultPool,
  healthLevel,
  metadataLabels,
  routeActionLabel,
  routeMatchLabel,
  vsBackendUuids,
  vsType,
  worstStatus,
} from './display';

describe('display', () => {
  it('gives the type and the distinct backends of a virtual server', () => {
    const vs = create(VirtualServerSchema, {
      routeConfig: {
        case: 'httpRoutesConfig',
        value: {
          virtualHosts: [
            {
              routes: [
                { action: { case: 'backendUuid', value: 'a' } },
                { action: { case: 'directResponse', value: { status: 200 } } },
              ],
            },
            {
              routes: [
                { action: { case: 'backendUuid', value: 'a' } },
                { action: { case: 'backendUuid', value: 'b' } },
              ],
            },
          ],
        },
      },
    });
    expect(vsType(vs)).toBe('HTTP');
    expect(vsBackendUuids(vs)).toEqual(['a', 'b']);

    const tcp = create(VirtualServerSchema, {
      routeConfig: { case: 'tcpRouteConfig', value: { backendUuid: 'c' } },
    });
    expect(vsType(tcp)).toBe('TCP');
    expect(vsBackendUuids(tcp)).toEqual(['c']);
  });

  it('lists the external auth backends after the route backends, once', () => {
    const vs = create(VirtualServerSchema, {
      routeConfig: {
        case: 'httpRoutesConfig',
        value: {
          virtualHosts: [{ routes: [{ action: { case: 'backendUuid', value: 'app' } }] }],
          externalAuths: [
            { name: 'sso', backendUuid: 'auth' },
            { name: 'other', backendUuid: 'app' },
          ],
        },
      },
    });
    expect(vsBackendUuids(vs)).toEqual(['app', 'auth']);
  });

  it('tells whether a virtual server accepts the proxy protocol, none for udp', () => {
    const vs = (routeConfig: unknown) => create(VirtualServerSchema, { routeConfig } as never);

    expect(
      acceptsProxyProtocol(vs({ case: 'httpRoutesConfig', value: { acceptProxyProtocol: true } })),
    ).toBe(true);
    expect(acceptsProxyProtocol(vs({ case: 'tlsTcpRoutesConfig', value: {} }))).toBe(false);
    expect(
      acceptsProxyProtocol(vs({ case: 'tcpRouteConfig', value: { acceptProxyProtocol: true } })),
    ).toBe(true);
    expect(acceptsProxyProtocol(vs({ case: 'udpRouteConfig', value: {} }))).toBeNull();
  });

  it('names the backend type from its endpoint configuration', () => {
    expect(backendType(create(BackendSchema, { endpoint: { case: 'dns', value: {} } }))).toBe(
      'DNS',
    );
    expect(
      backendType(create(BackendSchema, { endpoint: { case: 'ipAddrGroup', value: {} } })),
    ).toBe('Static');
    expect(backendType(create(BackendSchema))).toBe('Unknown');
  });

  it('describes sub routes', () => {
    const redirect = create(RouteSchema, {
      match: { pathSpecifier: { case: 'prefix', value: '/api' }, caseSensitive: true },
      action: {
        case: 'redirect',
        value: {
          schemeRewriteSpecifier: { case: 'httpsRedirect', value: true },
          hostRedirect: 'example.com',
          portRedirect: 8443,
        },
      },
    });
    expect(routeMatchLabel(redirect)).toBe('Prefix: /api (Case Sensitive)');
    expect(routeActionLabel(redirect)).toBe('Redirect to https://example.com:8443/*');

    const direct = create(RouteSchema, {
      action: { case: 'directResponse', value: { status: 404 } },
    });
    expect(routeActionLabel(direct)).toBe('Direct response with Status: 404');
    expect(routeActionLabel(create(RouteSchema))).toBe('None');
    const toBackend = create(RouteSchema, { action: { case: 'backendUuid', value: 'c1' } });
    expect(routeActionLabel(toBackend)).toBe('Route to backend');
  });

  it('colors healthy counts and keeps the worst status', () => {
    expect(healthLevel(2, 2)).toBe('ok');
    expect(healthLevel(1, 2)).toBe('warning');
    expect(healthLevel(0, 2)).toBe('error');
    expect(worstStatus([Status.OK, Status.WARNING, Status.ERRORED, Status.UNKNOWN])).toBe(
      Status.ERRORED,
    );
    expect(worstStatus([])).toBe(Status.OK);
  });

  it('gives the first public pool as the default pool', () => {
    const pools = [
      create(IpPoolSchema, { name: 'internal', privacy: IpPool_Privacy.PRIVATE }),
      create(IpPoolSchema, { name: 'edge', privacy: IpPool_Privacy.PUBLIC }),
      create(IpPoolSchema, { name: 'other', privacy: IpPool_Privacy.PUBLIC }),
    ];

    expect(defaultPool(pools)?.name).toBe('edge');
  });

  it('gives no default pool when every pool is private', () => {
    const pools = [create(IpPoolSchema, { name: 'internal', privacy: IpPool_Privacy.PRIVATE })];

    expect(defaultPool(pools)).toBeUndefined();
  });

  it('lists the metadata as sorted key=value', () => {
    expect(metadataLabels({ ns: 'a', env: 'prod' })).toEqual(['env=prod', 'ns=a']);
  });
});
