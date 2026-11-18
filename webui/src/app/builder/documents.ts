import { create, toJson, type DescMessage, type Message } from '@bufbuild/protobuf';
import { stringify } from 'yaml';
import {
  BackendSchema,
  DNS_LookupFamily,
  DNSSchema,
  EDSSchema,
  type Backend,
} from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VIPSchema, type VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import {
  TlsPassthroughSchema,
  TlsTcpRouteConfigSchema,
  TlsTcpRoutesConfigSchema,
  HttpRoutesConfigSchema,
  TcpRouteConfigSchema,
  UdpRouteConfigSchema,
  VirtualServerSchema,
  type VirtualServer,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import {
  RouteMatchSchema,
  RouteSchema,
  VirtualHostSchema,
} from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import {
  HealthCheckSchema,
  HttpHealthCheckSchema,
  NoHealthCheckSchema,
  TcpHealthCheckSchema,
} from '../../gen/flock/api/config/core/v1/healthcheck_pb';
import { IPAddrGroupSchema, IPAddrSchema } from '../../gen/flock/api/config/core/v1/ip_addr_pb';
import { TLSSchema } from '../../gen/flock/api/config/core/v1/tls_pb';
import {
  URLSchema,
  URL_UserInfoSchema,
  type URL as UrlMessage,
} from '../../gen/flock/api/config/core/v1/url_pb';
import { SecretKeyRefSchema } from '../../gen/flock/type/v1/secret_pb';
import { registry } from '../core/registry';
import { parseSecretRef } from '../core/secret-refs';

export type Protocol = 'http' | 'https' | 'tls' | 'tcp' | 'udp';

// Discovery is how a new backend finds its endpoints, the case of its endpoint oneof
export type Discovery = 'list' | 'dns' | 'eds';

// BackendInput keeps the fields of every discovery, so switching between them and back keeps what was typed
export interface BackendInput {
  name: string;
  discovery: Discovery;
  endpoints: { ip: string; port: number; weight: number }[];
  dns: { host: string; port: number };
  // passwordRef is the secret://<secret uuid>/<key> form of the picked password, empty for none
  eds: { url: string; serviceName: string; passwordRef: string };
  tls: boolean;
  healthCheck: { type: 'none' | 'tcp' | 'http'; path: string };
}

export interface WizardInput {
  vs: {
    name: string;
    protocol: Protocol;
    port: number;
    domains: string[];
    certificateUuid: string;
    passthrough: boolean;
  };
  backend: { existingUuid: string } | { create: BackendInput };
}

// NewVipInput is a VIP the run creates, with an ip given by hand or acquired from the ipam in the zone of an instance
// or in a zone
export interface NewVipInput {
  name: string;
  way: 'ipam' | 'explicit';
  ip: string;
  target: 'instance' | 'zone';
  instanceUuid: string;
  zoneUuid: string;
  // pool of the zone, the default pool of the zone when empty
  pool: string;
}

// Refs are what the virtual server writes to point to its backend and certificate. A file names them for flock apply,
// a direct creation gives their uuids.
export interface Refs {
  backend: string;
  certificate: string;
}

export interface Problem {
  field: string;
  message: string;
}

export interface TakenNames {
  vs: Set<string>;
  backend: Set<string>;
}

export type DocKind = 'vs' | 'backend' | 'vip';

const MAX_PORT = 65535;
const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
// A loose IPv6 shape check catches typos without a full parser.
const IPV6 = /^[0-9a-fA-F:]+(:\d{1,3}(\.\d{1,3}){3})?$/;

export function isIp(value: string): boolean {
  const isIpv6 = value.includes(':') && value.split(':').length > 2 && IPV6.test(value);
  return IPV4.test(value) || isIpv6;
}

function isPort(value: number): boolean {
  return Number.isInteger(value) && value >= 1 && value <= MAX_PORT;
}

const edsSchemes = ['http', 'https', 'grpc', 'grpcs'];

function parseUrl(text: string): URL | null {
  try {
    return new URL(text.trim());
  } catch {
    return null;
  }
}

// edsUrlHoldsPassword tells a url typed with `user:password@`, which the api refuses: a password only comes from a
// secret.
function edsUrlHoldsPassword(text: string): boolean {
  return (parseUrl(text)?.password ?? '') !== '';
}

// edsUrl splits the url of a discovery server in the parts of its message, null when it is not one or holds a
// password. A path of `/` is dropped because with grpc the path is put before the procedure.
export function edsUrl(text: string): UrlMessage | null {
  const url = parseUrl(text);
  if (url === null || url.password !== '') {
    return null;
  }
  const scheme = url.protocol.replace(/:$/, '');
  const isEdsUrl = edsSchemes.includes(scheme) && url.hostname !== '';
  if (!isEdsUrl) {
    return null;
  }
  return create(URLSchema, {
    scheme,
    host: url.hostname.replace(/^\[(.*)\]$/, '$1'),
    port: url.port === '' ? 0 : Number(url.port),
    path: url.pathname === '/' ? '' : url.pathname,
    userInfo:
      url.username !== ''
        ? create(URL_UserInfoSchema, { user: decodeURIComponent(url.username) })
        : undefined,
  });
}

// withPassword sets the password of a url with a user to the picked secret value
function withPassword(url: UrlMessage | null, passwordRef: string): UrlMessage | null {
  const ref = parseSecretRef(passwordRef);
  if (url?.userInfo === undefined || ref === null) {
    return url;
  }
  url.userInfo.passwordSource = {
    case: 'passwordFromSecret',
    value: create(SecretKeyRefSchema, { secretUuid: ref.secretUuid, key: ref.key }),
  };
  return url;
}

function backendEndpoint(spec: BackendInput): Backend['endpoint'] {
  switch (spec.discovery) {
    case 'dns':
      return {
        case: 'dns',
        value: create(DNSSchema, {
          host: spec.dns.host.trim(),
          port: spec.dns.port,
          // most services still answer on ipv4 first, the proto default asks AAAA records first
          lookupFamily: DNS_LookupFamily.V4_PREFERRED,
        }),
      };
    case 'eds':
      return {
        case: 'eds',
        value: create(EDSSchema, {
          url: withPassword(edsUrl(spec.eds.url), spec.eds.passwordRef) ?? undefined,
          serviceName: spec.eds.serviceName.trim(),
        }),
      };
    case 'list':
      return {
        case: 'ipAddrGroup',
        value: create(IPAddrGroupSchema, {
          ipAddresses: spec.endpoints.map((e) =>
            create(IPAddrSchema, { ip: e.ip.trim(), port: e.port, loadBalancingWeight: e.weight }),
          ),
        }),
      };
  }
}

export function buildDocuments(
  input: WizardInput,
  refs: Refs,
): { backend?: Backend; vs: VirtualServer } {
  const { vs } = input;
  const backendRef = refs.backend;
  const certificates = refs.certificate ? [refs.certificate] : [];

  const vsMessage = create(VirtualServerSchema, { name: vs.name.trim(), port: vs.port });

  switch (vs.protocol) {
    case 'http':
    case 'https': {
      const host = create(VirtualHostSchema, {
        domains: vs.domains.length > 0 ? vs.domains : ['*'],
        routes: [
          create(RouteSchema, {
            name: 'default',
            match: create(RouteMatchSchema, { pathSpecifier: { case: 'prefix', value: '/' } }),
            action: { case: 'backendUuid', value: backendRef },
          }),
        ],
        tlsOptions:
          vs.protocol === 'https'
            ? [
                create(TLSSchema, {
                  // a TLS server name can't be a bare *, no server name already takes every one
                  serverNames: vs.domains.filter((domain) => domain !== '*'),
                  certificatesUuid: certificates,
                }),
              ]
            : [],
      });
      vsMessage.routeConfig = {
        case: 'httpRoutesConfig',
        value: create(HttpRoutesConfigSchema, { virtualHosts: [host] }),
      };
      break;
    }
    case 'tls': {
      const route = create(TlsTcpRouteConfigSchema, {
        backendUuid: backendRef,
        mode: vs.passthrough
          ? {
              case: 'passthrough',
              value: create(TlsPassthroughSchema, { serverNames: vs.domains }),
            }
          : { case: 'tls', value: create(TLSSchema, { certificatesUuid: certificates }) },
      });
      vsMessage.routeConfig = {
        case: 'tlsTcpRoutesConfig',
        value: create(TlsTcpRoutesConfigSchema, { routes: [route] }),
      };
      break;
    }
    case 'tcp':
      vsMessage.routeConfig = {
        case: 'tcpRouteConfig',
        value: create(TcpRouteConfigSchema, { backendUuid: backendRef }),
      };
      break;
    case 'udp':
      vsMessage.routeConfig = {
        case: 'udpRouteConfig',
        value: create(UdpRouteConfigSchema, { backendUuid: backendRef }),
      };
      break;
  }

  if (!('create' in input.backend)) return { vs: vsMessage };

  const spec = input.backend.create;
  const checker = {
    none: { case: 'noHealthCheck' as const, value: create(NoHealthCheckSchema) },
    tcp: { case: 'tcpHealthCheck' as const, value: create(TcpHealthCheckSchema) },
    http: {
      case: 'httpHealthCheck' as const,
      value: create(HttpHealthCheckSchema, { path: spec.healthCheck.path }),
    },
  }[spec.healthCheck.type];

  // refresh_rate and the health check thresholds are left out on purpose, the controller gives them defaults.
  const backend = create(BackendSchema, {
    name: spec.name.trim(),
    enableTls: spec.tls,
    endpoint: backendEndpoint(spec),
    healthCheck: create(HealthCheckSchema, { healthChecker: checker }),
  });
  return { backend, vs: vsMessage };
}

export function fieldProblems(input: WizardInput, taken: TakenNames): Problem[] {
  const problems: Problem[] = [];
  const add = (field: string, message: string) => problems.push({ field, message });
  const { vs } = input;

  if (vs.name.trim() === '') add('vs.name', 'name is required');
  else if (taken.vs.has(vs.name.trim()))
    add('vs.name', 'already used by a virtual server of the controller');

  if (!isPort(vs.port)) add('vs.port', 'port must be between 1 and 65535');

  const needsCertificate = vs.protocol === 'https' || (vs.protocol === 'tls' && !vs.passthrough);
  if (needsCertificate && vs.certificateUuid === '')
    add('vs.certificate', 'a certificate is required');

  if ('existingUuid' in input.backend) {
    if (input.backend.existingUuid === '') add('backend.existing', 'pick a backend');
    return problems;
  }

  const backend = input.backend.create;
  if (backend.name.trim() === '') add('backend.name', 'name is required');
  else if (taken.backend.has(backend.name.trim()))
    add('backend.name', 'already used by a backend of the controller');

  switch (backend.discovery) {
    case 'list':
      if (backend.endpoints.length === 0)
        add('backend.endpoints', 'at least one endpoint is required');
      backend.endpoints.forEach((endpoint, i) => {
        if (!isIp(endpoint.ip.trim())) add(`backend.endpoints.${i}.ip`, 'not a valid ip address');
        if (!isPort(endpoint.port))
          add(`backend.endpoints.${i}.port`, 'port must be between 1 and 65535');
      });
      break;
    case 'dns':
      if (backend.dns.host.trim() === '') add('backend.dns.host', 'host is required');
      if (!isPort(backend.dns.port)) add('backend.dns.port', 'port must be between 1 and 65535');
      break;
    case 'eds':
      if (edsUrlHoldsPassword(backend.eds.url))
        add('backend.eds.url', 'a password can only come from a secret, remove it from the url');
      else if (edsUrl(backend.eds.url) === null)
        add('backend.eds.url', 'not a url with the scheme http, https, grpc or grpcs');
      else if (backend.eds.passwordRef !== '' && edsUrl(backend.eds.url)?.userInfo === undefined)
        add('backend.eds.url', 'a password needs a user, write it in the url as user@host');
      break;
  }

  const { healthCheck } = backend;
  if (healthCheck.type === 'http' && !healthCheck.path.startsWith('/')) {
    add('backend.healthCheck.path', 'path must start with /');
  }
  return problems;
}

export function vipProblems(vip: NewVipInput, takenNames: Set<string>): Problem[] {
  const problems: Problem[] = [];
  const add = (field: string, message: string) => problems.push({ field, message });
  const name = vip.name.trim();
  if (name === '') add('vip.name', 'name is required');
  else if (takenNames.has(name)) add('vip.name', 'already used by a VIP of the controller');

  if (vip.way === 'explicit') {
    if (!isIp(vip.ip.trim())) add('vip.ip', 'not a valid ip address');
    return problems;
  }
  if (vip.target === 'instance' && vip.instanceUuid === '')
    add('vip.instanceUuid', 'pick an instance');
  if (vip.target === 'zone' && vip.zoneUuid === '') add('vip.zoneUuid', 'pick a zone');
  return problems;
}

const SCHEMAS: Record<DocKind, DescMessage> = {
  vs: VirtualServerSchema,
  backend: BackendSchema,
  vip: VIPSchema,
};

export function kindDocument(kind: DocKind, message: Message): string {
  // the registry resolves the Any fields, such as the typed_announce of a VIP read from the controller
  const json = toJson(SCHEMAS[kind], message, { useProtoFieldName: true, registry });
  return stringify({ kind, ...(json as object) });
}

// newFile writes the documents of a run as a new resource file. The VIP gets a document without UUID listing the
// virtual servers of the run after its own, flock apply finds the VIP by name.
export function newFile(docs: { kind: DocKind; message: Message }[], vip?: VIP): string {
  const written = docs.map((d) => kindDocument(d.kind, d.message));
  if (vip) {
    const newRefs = docs
      .filter((d) => d.kind === 'vs')
      .map((d) => `name:${(d.message as VirtualServer).name}`);
    const entries = [...vip.virtualServersUuid, ...newRefs];
    written.push(
      kindDocument('vip', create(VIPSchema, { ...vip, UUID: '', virtualServersUuid: entries })),
    );
  }
  return written.join('---\n');
}
