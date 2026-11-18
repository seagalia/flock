import { KindName } from '../core/resources';

// One section of a resource in its detail panel. fields are proto field or oneof names, in the order shown.
export interface CardDef {
  title: string;
  fields: string[];
  // the Any extension fields, whose options depend on the agents serving the resource
  advanced?: boolean;
}

const metadata: CardDef = { title: 'Metadata', fields: ['metadata'] };

// Every field of a kind sits in exactly one card (a spec checks it), so a new proto field shows up only once it
// is placed here.
export const cards: Record<KindName, CardDef[]> = {
  backend: [
    { title: 'General', fields: ['UUID', 'name', 'lb_policy', 'connect_timeout', 'refresh_rate'] },
    { title: 'Endpoints', fields: ['endpoint'] },
    { title: 'TLS', fields: ['enable_tls', 'sni', 'ca'] },
    { title: 'Health checks', fields: ['health_check'] },
    { title: 'Proxy protocol', fields: ['enable_proxy_protocol', 'proxy_protocol_version'] },
    { title: 'Advanced', fields: ['http_protocol_options', 'typed_options'], advanced: true },
    metadata,
  ],
  certificate: [
    { title: 'General', fields: ['UUID', 'name', 'domain_names'] },
    { title: 'Certificate', fields: ['cert_chain', 'private_key', 'ca'] },
    metadata,
  ],
  secret: [
    { title: 'General', fields: ['UUID', 'name'] },
    { title: 'Data', fields: ['data'] },
    metadata,
  ],
  vip: [
    { title: 'General', fields: ['UUID', 'name', 'ip', 'prefix_length'] },
    { title: 'Virtual servers', fields: ['virtual_servers_uuid'] },
    { title: 'Advanced', fields: ['typed_announce'], advanced: true },
    metadata,
  ],
  vs: [
    { title: 'General', fields: ['UUID', 'name', 'port', 'enable_l3_log'] },
    { title: 'Routes', fields: ['route_config'] },
    { title: 'Limits', fields: ['limit_config'] },
    metadata,
  ],
  instance: [
    { title: 'General', fields: ['UUID', 'name', 'zone_uuid', 'resource_type'] },
    { title: 'VIPs', fields: ['vips_uuid'] },
    metadata,
  ],
  zone: [
    { title: 'General', fields: ['UUID', 'name', 'zone'] },
    { title: 'Pools', fields: ['pools'] },
    metadata,
  ],
  // endpoints are statuses of a backend, they have no panel
  endpoint: [],
};
