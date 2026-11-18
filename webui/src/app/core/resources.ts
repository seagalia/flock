import { create, DescMessage, Message, toJson } from '@bufbuild/protobuf';
import { CertificateSchema } from '../../gen/flock/api/config/cert/v1/cert_pb';
import {
  BackendEndpointStatsSchema,
  BackendSchema,
} from '../../gen/flock/api/config/backend/v1/backend_pb';
import { InstanceSchema } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { SecretSchema } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { ZoneSchema } from '../../gen/flock/api/config/zone/v1/zone_pb';
import {
  ListFilter,
  ListFilter_Operator,
  ListFilterSchema,
} from '../../gen/flock/services/core/v1/filter_pb';
import { registry } from './registry';

export type KindName =
  | 'instance'
  | 'backend'
  | 'certificate'
  | 'secret'
  | 'vip'
  | 'vs'
  | 'zone'
  | 'endpoint';

export interface Kind {
  name: KindName;
  schema: DescMessage;
  label: string;
  // route of the page listing this kind, relative to the base href
  link: string;
  // flock command reading one resource of this kind, empty for kinds the cli does not read
  cli: string;
  // whether the controller keeps member permissions on resources of this kind, zones have none
  hasPermissions: boolean;
}

export const kinds: Record<KindName, Kind> = {
  endpoint: {
    name: 'endpoint',
    schema: BackendEndpointStatsSchema,
    label: 'Endpoint',
    link: '/',
    cli: '',
    hasPermissions: false,
  },
  vs: {
    name: 'vs',
    schema: VirtualServerSchema,
    label: 'Virtual Server',
    link: '/virtualservers',
    cli: 'vs',
    hasPermissions: true,
  },
  backend: {
    name: 'backend',
    schema: BackendSchema,
    label: 'Backend',
    link: '/backends',
    cli: 'backend',
    hasPermissions: true,
  },
  certificate: {
    name: 'certificate',
    schema: CertificateSchema,
    label: 'Certificate',
    link: '/certificates',
    cli: 'certificate',
    hasPermissions: true,
  },
  secret: {
    name: 'secret',
    schema: SecretSchema,
    label: 'Secret',
    link: '/secrets',
    cli: 'secret',
    hasPermissions: true,
  },
  vip: {
    name: 'vip',
    schema: VIPSchema,
    label: 'VIP',
    link: '/vips',
    cli: 'vip',
    hasPermissions: true,
  },
  instance: {
    name: 'instance',
    schema: InstanceSchema,
    label: 'Instance',
    link: '/instances',
    cli: 'instance',
    hasPermissions: true,
  },
  zone: {
    name: 'zone',
    schema: ZoneSchema,
    label: 'Zone',
    link: '/zones',
    cli: 'zone',
    hasPermissions: false,
  },
};

// kindWord names a kind inside a sentence, an acronym such as VIP keeps its capitals
export function kindWord(kind: KindName): string {
  const label = kinds[kind].label;
  return label === label.toUpperCase() ? label : label.toLowerCase();
}

export const typeUrlPrefix = 'type.googleapis.com/';

// The controller names resource types by their type url, in filters and permissions
export function typeUrl(kind: Kind): string {
  return typeUrlPrefix + kind.schema.typeName;
}

export function kindOf(message: Message): Kind {
  const kind = Object.values(kinds).find((k) => k.schema.typeName === message.$typeName);
  if (!kind) {
    throw new Error(`unknown resource type ${message.$typeName}`);
  }
  return kind;
}

// A resource as the tables see it, identified by its UUID, or by its name when it has none
export interface Resource extends Message {
  name: string;
  UUID?: string;
}

export function resourceId(resource: Resource): string {
  return resource.UUID || resource.name;
}

// cliCommand gives the flock command printing the resource, null for what the cli does not read
export function cliCommand(kind: Kind, resource: Resource): string | null {
  if (!kind.cli) {
    return null;
  }
  // a name with spaces or shell characters stays one argument
  const plainName = /^[\w./:@-]+$/.test(resource.name);
  const name = plainName ? resource.name : `'${resource.name.replaceAll("'", "'\\''")}'`;
  return `flock ${kind.cli} ${name}`;
}

const uuidRegex = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isUuid(value: string): boolean {
  return uuidRegex.test(value);
}

export function nameOrUuidFilter(search: string): ListFilter {
  if (isUuid(search)) {
    return create(ListFilterSchema, {
      key: 'uuid',
      value: search,
      operator: ListFilter_Operator.EQUAL,
    });
  }
  return create(ListFilterSchema, {
    key: 'name',
    value: search,
    operator: ListFilter_Operator.LIKE,
  });
}

// matchesSearch applies to an already loaded list the filters of nameOrUuidFilter, as the api would
export function matchesSearch(resource: Resource, filters: ListFilter[]): boolean {
  return filters.every((filter) => {
    if (filter.key === 'uuid') {
      return resourceId(resource) === filter.value;
    }
    if (filter.key === 'name') {
      return resource.name.toLowerCase().includes(filter.value.toLowerCase());
    }
    return true;
  });
}

// Filters the resources linked to linkedTo, whatever their direction (a VS lists its VIPs and a VIP its VS).
// Several of these filters on the same list are or-ed.
export function linkedToFilter(linkedTo: Resource): ListFilter {
  return create(ListFilterSchema, {
    key: typeUrl(kindOf(linkedTo)),
    value: resourceId(linkedTo),
    operator: ListFilter_Operator.EQUAL,
  });
}

// What refuses the deletion of a kind, and what the "Used by" list of its panel shows
export const usedByKinds: Record<KindName, KindName[]> = {
  backend: ['vs'],
  certificate: ['vs'],
  secret: ['vs', 'backend', 'vip', 'certificate', 'zone', 'instance'],
  vs: ['vip'],
  vip: ['instance'],
  zone: ['instance'],
  instance: [],
  endpoint: [],
};

export const pageAllSize = 100;

// listAll pages through a list call until a page is not full, the api gives no total count
export async function listAll<T>(
  listPage: (limit: number, offset: number) => Promise<T[]>,
): Promise<T[]> {
  const all: T[] = [];
  for (;;) {
    const page = await listPage(pageAllSize, all.length);
    all.push(...page);
    if (page.length < pageAllSize) {
      return all;
    }
  }
}

export function messageToJson(message: Message): unknown {
  const schema = registry.getMessage(message.$typeName);
  if (!schema) {
    return message;
  }
  return toJson(schema, message, { registry, alwaysEmitImplicit: true });
}
