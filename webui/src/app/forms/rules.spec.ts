import { create } from '@bufbuild/protobuf';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import {
  TlsTcpRouteConfigSchema,
  TlsTcpRoutesConfigSchema,
  VirtualServerSchema,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { rulesProblems } from './rules';

describe('rulesProblems', () => {
  it('flags an empty required name and a port of 70000', () => {
    const vs = create(VirtualServerSchema, { name: '', port: 70000 });

    const problems = rulesProblems(VirtualServerSchema, vs);

    expect(problems).toContainEqual({
      path: 'name',
      message: 'value length must be at least 1 characters',
    });
    expect(problems).toContainEqual({
      path: 'port',
      message: 'value must be greater than 0 and less than or equal to 65535',
    });
  });

  it('flags an unset required oneof and an unset required message', () => {
    const problems = rulesProblems(BackendSchema, create(BackendSchema, { name: 'web' }));

    expect(problems).toContainEqual({
      path: 'endpoint',
      message: 'exactly one field is required in oneof',
    });
    expect(problems).toContainEqual({ path: 'health_check', message: 'value is required' });
  });

  it('names the items of a repeated message by their index', () => {
    const vs = create(VirtualServerSchema, {
      name: 'web',
      port: 443,
      routeConfig: {
        case: 'tlsTcpRoutesConfig',
        value: create(TlsTcpRoutesConfigSchema, {
          routes: [create(TlsTcpRouteConfigSchema, { mode: { case: 'passthrough', value: {} } })],
        }),
      },
    });

    expect(rulesProblems(VirtualServerSchema, vs)).toEqual([
      {
        path: 'tls_tcp_routes_config.routes[0].backend_uuid',
        message: 'value length must be at least 1 characters',
      },
    ]);
  });

  it('accepts a valid resource', () => {
    const vs = create(VirtualServerSchema, {
      name: 'web',
      port: 443,
      routeConfig: { case: 'tcpRouteConfig', value: { backendUuid: 'b' } },
    });

    expect(rulesProblems(VirtualServerSchema, vs)).toEqual([]);
  });

  it('leaves required out with skipRequired and keeps the other rules', () => {
    const backend = create(BackendSchema, { name: 'web' });
    const vs = create(VirtualServerSchema, { name: 'web', port: 70000 });

    expect(rulesProblems(BackendSchema, backend, '', { skipRequired: true })).toEqual([]);
    expect(rulesProblems(VirtualServerSchema, vs, '', { skipRequired: true })).toEqual([
      { path: 'port', message: 'value must be greater than 0 and less than or equal to 65535' },
    ]);
  });
});
