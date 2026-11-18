import { create, toJson, type DescMessage, type Message } from '@bufbuild/protobuf';
import { anyPack } from '@bufbuild/protobuf/wkt';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import {
  StarlingAnnounce_TypeAnnounce,
  StarlingAnnounceSchema,
} from '../../gen/flock/extensions/announces/starling_announce/v1/announce_pb';
import { registry } from '../core/registry';
import {
  buildDocuments,
  edsUrl,
  newFile,
  fieldProblems,
  vipProblems,
  type BackendInput,
  type NewVipInput,
  type Refs,
  type TakenNames,
  type WizardInput,
} from './documents';

const json = (schema: DescMessage, message: Message) =>
  toJson(schema, message, { useProtoFieldName: true, registry });

const newBackend: BackendInput = {
  name: 'web',
  discovery: 'list',
  endpoints: [{ ip: '10.0.0.10', port: 8080, weight: 0 }],
  dns: { host: '', port: 0 },
  eds: { url: '', serviceName: '', passwordRef: '' },
  tls: false,
  healthCheck: { type: 'tcp', path: '' },
};

function input(vs: Partial<WizardInput['vs']> = {}, backend?: WizardInput['backend']): WizardInput {
  return {
    vs: {
      name: 'web',
      protocol: 'http',
      port: 80,
      domains: [],
      certificateUuid: '',
      passthrough: false,
      ...vs,
    },
    backend: backend ?? { create: newBackend },
  };
}

// named refers to the backend and the certificate the way a file does. The uuids these specs give are names.
function named(i: WizardInput): Refs {
  const backend = 'create' in i.backend ? i.backend.create.name.trim() : i.backend.existingUuid;
  const certificate = i.vs.certificateUuid;
  return { backend: `name:${backend}`, certificate: certificate ? `name:${certificate}` : '' };
}

const build = (i: WizardInput) => buildDocuments(i, named(i));

const nothingTaken = (): TakenNames => ({ vs: new Set(), backend: new Set() });

describe('buildDocuments', () => {
  it('builds an http virtual server with a wildcard host when no domain is given', () => {
    const { vs } = build(input());
    expect(json(VirtualServerSchema, vs)).toEqual({
      name: 'web',
      port: 80,
      http_routes_config: {
        virtual_hosts: [
          {
            domains: ['*'],
            routes: [{ name: 'default', match: { prefix: '/' }, backend_uuid: 'name:web' }],
          },
        ],
      },
    });
  });

  it('keeps the given domains', () => {
    const { vs } = build(input({ domains: ['a.example', 'b.example'] }));
    const host = json(VirtualServerSchema, vs) as {
      http_routes_config: { virtual_hosts: { domains: string[] }[] };
    };
    expect(host.http_routes_config.virtual_hosts[0].domains).toEqual(['a.example', 'b.example']);
  });

  it('adds tls options with the certificate reference on https', () => {
    const { vs } = build(
      input({ protocol: 'https', port: 443, domains: ['a.example'], certificateUuid: 'site' }),
    );
    expect(json(VirtualServerSchema, vs)).toEqual({
      name: 'web',
      port: 443,
      http_routes_config: {
        virtual_hosts: [
          {
            domains: ['a.example'],
            tls_options: [{ server_names: ['a.example'], certificates_uuid: ['name:site'] }],
            routes: [{ name: 'default', match: { prefix: '/' }, backend_uuid: 'name:web' }],
          },
        ],
      },
    });
  });

  it('builds a tls route that terminates with the certificate', () => {
    const { vs } = build(input({ protocol: 'tls', port: 443, certificateUuid: 'site' }));
    expect(json(VirtualServerSchema, vs)).toEqual({
      name: 'web',
      port: 443,
      tls_tcp_routes_config: {
        routes: [{ backend_uuid: 'name:web', tls: { certificates_uuid: ['name:site'] } }],
      },
    });
  });

  it('builds a tls route in passthrough with the server names', () => {
    const { vs } = build(
      input({ protocol: 'tls', port: 443, passthrough: true, domains: ['a.example'] }),
    );
    expect(json(VirtualServerSchema, vs)).toEqual({
      name: 'web',
      port: 443,
      tls_tcp_routes_config: {
        routes: [{ backend_uuid: 'name:web', passthrough: { server_names: ['a.example'] } }],
      },
    });
  });

  it('builds tcp and udp routes', () => {
    const tcp = build(input({ protocol: 'tcp', port: 5432 })).vs;
    const udp = build(input({ protocol: 'udp', port: 53 })).vs;
    expect(json(VirtualServerSchema, tcp)).toEqual({
      name: 'web',
      port: 5432,
      tcp_route_config: { backend_uuid: 'name:web' },
    });
    expect(json(VirtualServerSchema, udp)).toEqual({
      name: 'web',
      port: 53,
      udp_route_config: { backend_uuid: 'name:web' },
    });
  });

  it('refers to the backend and the certificate by the uuids it is given', () => {
    const { vs } = buildDocuments(
      input({ protocol: 'https', port: 443, certificateUuid: 'site' }),
      {
        backend: 'backend-1',
        certificate: 'cert-1',
      },
    );
    expect(json(VirtualServerSchema, vs)).toMatchObject({
      http_routes_config: {
        virtual_hosts: [
          {
            routes: [{ backend_uuid: 'backend-1' }],
            tls_options: [{ certificates_uuid: ['cert-1'] }],
          },
        ],
      },
    });
  });

  it('refers to an existing backend and creates none', () => {
    const built = build(input({ protocol: 'tcp' }, { existingUuid: 'shared' }));
    expect(built.backend).toBeUndefined();
    expect(json(VirtualServerSchema, built.vs)).toMatchObject({
      tcp_route_config: { backend_uuid: 'name:shared' },
    });
  });

  it('builds a backend with each health check type', () => {
    const backendOf = (healthCheck: BackendInput['healthCheck']) =>
      json(BackendSchema, build(input({}, { create: { ...newBackend, healthCheck } })).backend!);

    expect(backendOf({ type: 'tcp', path: '' })).toEqual({
      name: 'web',
      ip_addr_group: { ip_addresses: [{ ip: '10.0.0.10', port: 8080 }] },
      health_check: { tcp_health_check: {} },
    });
    expect(backendOf({ type: 'none', path: '' })).toMatchObject({
      health_check: { no_health_check: {} },
    });
    expect(backendOf({ type: 'http', path: '/up' })).toMatchObject({
      health_check: { http_health_check: { path: '/up' } },
    });
  });

  it('builds a dns backend from its host and port, v4 preferred, with no endpoint list', () => {
    const backend = build(
      input(
        {},
        { create: { ...newBackend, discovery: 'dns', dns: { host: ' web.local ', port: 80 } } },
      ),
    ).backend!;
    expect(json(BackendSchema, backend)).toEqual({
      name: 'web',
      dns: { host: 'web.local', port: 80, lookup_family: 'V4_PREFERRED' },
      health_check: { tcp_health_check: {} },
    });
  });

  it('builds an eds backend with its url split in parts', () => {
    const eds = {
      url: 'grpcs://lb@vigie.internal:8443/eds',
      serviceName: ' shop ',
      passwordRef: '',
    };
    const backend = build(input({}, { create: { ...newBackend, discovery: 'eds', eds } })).backend!;
    expect(json(BackendSchema, backend)).toMatchObject({
      eds: {
        url: {
          scheme: 'grpcs',
          host: 'vigie.internal',
          port: 8443,
          path: '/eds',
          user_info: { user: 'lb' },
        },
        service_name: 'shop',
      },
    });
    expect(json(BackendSchema, backend)).not.toHaveProperty('ip_addr_group');
  });

  it('takes the eds password from the picked secret', () => {
    const eds = {
      url: 'grpcs://lb@vigie.internal:8443/eds',
      serviceName: 'shop',
      passwordRef: 'secret://s1/k',
    };
    const backend = build(input({}, { create: { ...newBackend, discovery: 'eds', eds } })).backend!;
    expect(json(BackendSchema, backend)).toMatchObject({
      eds: {
        url: { user_info: { user: 'lb', password_from_secret: { secret_uuid: 's1', key: 'k' } } },
      },
    });
  });

  it('keeps * out of the TLS server names of an https virtual host', () => {
    const built = build(input({ protocol: 'https', domains: ['*'], certificateUuid: 'site' }));
    expect(json(VirtualServerSchema, built.vs)).toMatchObject({
      http_routes_config: {
        virtual_hosts: [{ domains: ['*'], tls_options: [{ certificates_uuid: ['name:site'] }] }],
      },
    });
    const host = json(VirtualServerSchema, built.vs) as {
      http_routes_config: { virtual_hosts: { tls_options: { server_names?: string[] }[] }[] };
    };
    expect(host.http_routes_config.virtual_hosts[0].tls_options[0].server_names).toBeUndefined();
  });

  it('writes the weight and enable_tls of a backend', () => {
    const backend = build(
      input(
        {},
        { create: { ...newBackend, tls: true, endpoints: [{ ip: '::1', port: 443, weight: 5 }] } },
      ),
    ).backend!;
    expect(json(BackendSchema, backend)).toMatchObject({
      enable_tls: true,
      ip_addr_group: { ip_addresses: [{ ip: '::1', port: 443, load_balancing_weight: 5 }] },
    });
  });
});

describe('fieldProblems', () => {
  const fields = (i: WizardInput, taken = nothingTaken()) =>
    fieldProblems(i, taken).map((p) => p.field);

  it('treats names with surrounding spaces as the trimmed names', () => {
    const taken = { vs: new Set(['web']), backend: new Set(['web']) };
    expect(
      fields(input({ name: ' web ' }, { create: { ...newBackend, name: ' web' } }), taken),
    ).toEqual(['vs.name', 'backend.name']);
    const { vs, backend } = build(
      input({ name: ' web ' }, { create: { ...newBackend, name: ' web' } }),
    );
    expect(vs.name).toBe('web');
    expect(backend?.name).toBe('web');
    expect(json(VirtualServerSchema, vs)).toMatchObject({ http_routes_config: expect.anything() });
  });

  it('accepts a complete input', () => {
    expect(fieldProblems(input(), nothingTaken())).toEqual([]);
  });

  it('flags an empty virtual server name and a bad port', () => {
    expect(fieldProblems(input({ name: ' ', port: 0 }), nothingTaken())).toEqual([
      { field: 'vs.name', message: 'name is required' },
      { field: 'vs.port', message: 'port must be between 1 and 65535' },
    ]);
    expect(fields(input({ port: 65536 }))).toEqual(['vs.port']);
    expect(fields(input({ port: 80.5 }))).toEqual(['vs.port']);
  });

  it('refuses names taken on the controller', () => {
    const taken = { vs: new Set(['web']), backend: new Set(['web']) };
    expect(fieldProblems(input(), taken)).toEqual([
      { field: 'vs.name', message: 'already used by a virtual server of the controller' },
      { field: 'backend.name', message: 'already used by a backend of the controller' },
    ]);
  });

  it('does not check the name of an existing backend against the taken ones', () => {
    const taken = { vs: new Set<string>(), backend: new Set(['shared']) };
    expect(fieldProblems(input({}, { existingUuid: 'shared' }), taken)).toEqual([]);
    expect(fields(input({}, { existingUuid: '' }))).toEqual(['backend.existing']);
  });

  it('asks for a certificate on https and on tls without passthrough', () => {
    expect(fields(input({ protocol: 'https' }))).toEqual(['vs.certificate']);
    expect(fields(input({ protocol: 'tls' }))).toEqual(['vs.certificate']);
    expect(fields(input({ protocol: 'tls', passthrough: true }))).toEqual([]);
    expect(fields(input({ protocol: 'https', certificateUuid: 'site' }))).toEqual([]);
    expect(fields(input({ protocol: 'http' }))).toEqual([]);
  });

  it('flags endpoints one by one', () => {
    const backend: BackendInput = {
      ...newBackend,
      endpoints: [
        { ip: '10.0.0.1', port: 80, weight: 0 },
        { ip: '10.0.0.256', port: 80, weight: 0 },
        { ip: '2001:db8::1', port: 0, weight: 0 },
        { ip: 'web.local', port: 80, weight: 0 },
      ],
    };
    expect(fields(input({}, { create: backend }))).toEqual([
      'backend.endpoints.1.ip',
      'backend.endpoints.2.port',
      'backend.endpoints.3.ip',
    ]);
  });

  it('asks for at least one endpoint', () => {
    expect(fields(input({}, { create: { ...newBackend, endpoints: [] } }))).toEqual([
      'backend.endpoints',
    ]);
  });

  it('checks the host and port of a dns backend only', () => {
    const dns = (host: string, port: number) =>
      input(
        {},
        { create: { ...newBackend, discovery: 'dns', endpoints: [], dns: { host, port } } },
      );
    expect(fields(dns('', 0))).toEqual(['backend.dns.host', 'backend.dns.port']);
    expect(fields(dns('web.local', 80))).toEqual([]);
  });

  it('checks the url of an eds backend', () => {
    const eds = (url: string) =>
      input(
        {},
        {
          create: {
            ...newBackend,
            discovery: 'eds',
            eds: { url, serviceName: '', passwordRef: '' },
          },
        },
      );
    expect(fields(eds('vigie:8080'))).toEqual(['backend.eds.url']);
    expect(fields(eds('ftp://vigie'))).toEqual(['backend.eds.url']);
    expect(fields(eds('http://vigie/services'))).toEqual([]);
  });

  it('asks for a user in the eds url once a password secret is picked', () => {
    const eds = (url: string) =>
      input(
        {},
        {
          create: {
            ...newBackend,
            discovery: 'eds',
            eds: { url, serviceName: '', passwordRef: 'secret://s1/k' },
          },
        },
      );
    expect(fields(eds('http://vigie/services'))).toEqual(['backend.eds.url']);
    expect(fields(eds('http://lb@vigie/services'))).toEqual([]);
  });

  it('refuses an eds url holding a password, which only a secret can give', () => {
    const eds = (url: string) =>
      input(
        {},
        {
          create: {
            ...newBackend,
            discovery: 'eds',
            eds: { url, serviceName: '', passwordRef: '' },
          },
        },
      );
    const problems = fieldProblems(eds('http://lb:pass@vigie/services'), nothingTaken());
    expect(problems.map((p) => p.field)).toEqual(['backend.eds.url']);
    expect(problems[0].message).toContain('secret');
    expect(edsUrl('http://lb:pass@vigie/services')).toBeNull();
  });

  it('asks for a health check path only for http checks', () => {
    const http = (path: string) =>
      input({}, { create: { ...newBackend, healthCheck: { type: 'http', path } } });
    expect(fields(http(''))).toEqual(['backend.healthCheck.path']);
    expect(fields(http('up'))).toEqual(['backend.healthCheck.path']);
    expect(fields(http('/up'))).toEqual([]);
  });
});

describe('vipProblems', () => {
  const newVip = (vip: Partial<NewVipInput> = {}): NewVipInput => ({
    name: 'public',
    way: 'explicit',
    ip: '192.0.2.10',
    target: 'instance',
    instanceUuid: '',
    zoneUuid: '',
    pool: '',
    ...vip,
  });
  const fields = (vip: NewVipInput, taken = new Set<string>()) =>
    vipProblems(vip, taken).map((p) => p.field);

  it('accepts a VIP with a name and an explicit ip', () => {
    expect(vipProblems(newVip(), new Set())).toEqual([]);
  });

  it('asks for a name no VIP of the controller has', () => {
    expect(vipProblems(newVip({ name: ' ' }), new Set())).toEqual([
      { field: 'vip.name', message: 'name is required' },
    ]);
    expect(vipProblems(newVip(), new Set(['public']))).toEqual([
      { field: 'vip.name', message: 'already used by a VIP of the controller' },
    ]);
  });

  it('checks the explicit ip only on the explicit way', () => {
    expect(fields(newVip({ ip: '10.0.0.300' }))).toEqual(['vip.ip']);
    expect(fields(newVip({ way: 'ipam', ip: '', instanceUuid: 'i1' }))).toEqual([]);
  });

  it('asks the ipam way for the instance or the zone it picked', () => {
    expect(fields(newVip({ way: 'ipam', target: 'instance' }))).toEqual(['vip.instanceUuid']);
    expect(fields(newVip({ way: 'ipam', target: 'zone' }))).toEqual(['vip.zoneUuid']);
    expect(fields(newVip({ way: 'ipam', target: 'zone', zoneUuid: 'z1' }))).toEqual([]);
  });
});

describe('newFile', () => {
  const built = build(input({ protocol: 'tcp', port: 5432 }));
  const docs = [
    { kind: 'backend' as const, message: built.backend! },
    { kind: 'vs' as const, message: built.vs },
  ];
  const written = [
    'kind: backend',
    'name: web',
    'ip_addr_group:',
    '  ip_addresses:',
    '    - ip: 10.0.0.10',
    '      port: 8080',
    'health_check:',
    '  tcp_health_check: {}',
    '---',
    'kind: vs',
    'name: web',
    'port: 5432',
    'tcp_route_config:',
    '  backend_uuid: name:web',
    '',
  ].join('\n');

  it('writes the documents, kind first', () => {
    expect(newFile(docs)).toBe(written);
  });

  const current = create(VIPSchema, {
    UUID: 'uuid-1',
    name: 'public',
    ip: '192.0.2.10',
    virtualServersUuid: ['6b0c1c9e-0f43-4c47-a0a7-0d1f1d7b8e21'],
  });

  it('adds a vip document without UUID listing the new virtual server after its own', () => {
    const text = newFile(docs, current);
    expect(text).toBe(
      `${written}---\n` +
        [
          'kind: vip',
          'name: public',
          'ip: 192.0.2.10',
          'virtual_servers_uuid:',
          '  - 6b0c1c9e-0f43-4c47-a0a7-0d1f1d7b8e21',
          '  - name:web',
          '',
        ].join('\n'),
    );
    expect(text).not.toContain('UUID:');
  });

  it('writes the typed_announce of the vip with its type', () => {
    const announce = create(StarlingAnnounceSchema, {
      typeAnnounce: StarlingAnnounce_TypeAnnounce.LAYER2,
    });
    const vip = create(VIPSchema, {
      UUID: 'vip-1',
      name: 'public',
      ip: '192.0.2.10',
      typedAnnounce: anyPack(StarlingAnnounceSchema, announce),
    });

    const text = newFile(docs, vip);

    expect(text).toContain(
      [
        'kind: vip',
        'name: public',
        'ip: 192.0.2.10',
        'virtual_servers_uuid:',
        '  - name:web',
        'typed_announce:',
        '  type_announce: LAYER2',
        '  "@type": type.googleapis.com/flock.extensions.announces.starling_announce.v1.StarlingAnnounce',
        '',
      ].join('\n'),
    );
  });
});

describe('edsUrl', () => {
  it('drops a lone / path, which grpc would put before the procedure', () => {
    expect(edsUrl('grpc://vigie:8080')).toMatchObject({
      scheme: 'grpc',
      host: 'vigie',
      port: 8080,
      path: '',
    });
    expect(edsUrl('http://vigie/')).toMatchObject({ scheme: 'http', port: 0, path: '' });
  });

  it('keeps an ipv6 host without its brackets', () => {
    expect(edsUrl('https://[2001:db8::1]:9000/services')).toMatchObject({
      host: '2001:db8::1',
      port: 9000,
      path: '/services',
    });
  });

  it('refuses what is not a url of a discovery server', () => {
    expect(edsUrl('')).toBeNull();
    expect(edsUrl('vigie:8080')).toBeNull();
    expect(edsUrl('ws://vigie')).toBeNull();
  });
});
