import { MatDialog } from '@angular/material/dialog';
import { create, equals } from '@bufbuild/protobuf';
import { ConnectError } from '@connectrpc/connect';
import { Instance } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { Secret } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { IpPool, IpPoolSchema, Zone } from '../../gen/flock/api/config/zone/v1/zone_pb';
import {
  RegisterVipRequest,
  RegisterVipRequestSchema,
} from '../../gen/flock/services/vip/v1/vip_pb';
import { Api, silentCall } from '../core/api';
import { KindName, Resource } from '../core/resources';
import { FieldError } from '../forms/rules';
import { violationsOf } from '../forms/violations';
import { confirm } from '../ui/confirm-dialog';
import { adminReason, ControllerFeatures } from './access';
import { confirmChange, fieldChanges } from './confirm-change';
import { isLocked, lockMetadataKey, lockReason } from './lock';
import { CreateWay, FieldsFailure, KindRules, lockKeyRefusal } from './resource-panel';

// The upstream header names the rule external_auth.upstream_headers.forbidden refuses. The browser checks no CEL rule,
// and the controller checks this one on the lowercased names.
const forbiddenUpstreamHeaders = new Set([
  'host',
  'connection',
  'transfer-encoding',
  'te',
  'upgrade',
  'keep-alive',
  'proxy-connection',
  'trailer',
  'content-length',
]);

// What the rules of a kind depend on, known when a page opens without any permission call
export interface RulesContext {
  api: Api;
  dialog: MatDialog;
  isAdmin: boolean;
  features: ControllerFeatures;
}

// kindRules are the exceptions of a kind to the generic panel, from the rules of the controller
export function kindRules(kind: KindName, context: RulesContext): KindRules {
  switch (kind) {
    case 'vip':
      return vipRules(context);
    case 'instance':
      return instanceRules(context);
    case 'zone':
      return zoneRules(context);
    case 'certificate':
      // Reads give an empty private_key and UpdateCertificate keeps the stored key when it gets an empty one, so an
      // edit that types no key sends the empty one back. A typed key stays out of the confirmations.
      return { masked: ['private_key'] };
    case 'secret':
      return secretRules();
    case 'vs':
      return { problems: (_, edited) => externalAuthProblems(edited as VirtualServer) };
    default:
      return {};
  }
}

// A read gives the keys of a secret with empty values and UpdateSecret keeps the stored value of a key sent empty, so
// the values never enter a confirmation. Only a key the secret doesn't hold yet must come with its value.
function secretRules(): KindRules {
  return {
    masked: ['data'],
    problems: (stored, edited) => {
      const storedKeys = Object.keys((stored as Secret | null)?.data ?? {});
      const keysWithoutValue = Object.entries((edited as Secret).data)
        .filter(([key, value]) => value === '' && !storedKeys.includes(key))
        .map(([key]) => key);
      if (keysWithoutValue.length === 0) {
        return [];
      }
      return [
        { path: 'data', message: `A new key needs a value: ${keysWithoutValue.join(', ')}.` },
      ];
    },
  };
}

// externalAuthProblems refuses what the controller refuses before the call. On the policies, a name defined twice and
// an upstream header it never accepts or that the check reads (authorization or one of its request headers, removed
// before the check is sent). On the virtual hosts and routes, a policy name external_auths doesn't define.
function externalAuthProblems(vs: VirtualServer): FieldError[] {
  if (vs.routeConfig.case !== 'httpRoutesConfig') {
    return [];
  }
  const config = vs.routeConfig.value;
  const prefix = 'http_routes_config';
  const problems: FieldError[] = [];
  const defined = new Set<string>();
  config.externalAuths.forEach((policy, index) => {
    if (defined.has(policy.name)) {
      problems.push({
        path: `${prefix}.external_auths[${index}].name`,
        message: `external auth ${policy.name} is defined twice`,
      });
    }
    defined.add(policy.name);
    const sentToCheck = new Set([
      'authorization',
      ...policy.requestHeaders.map((h) => h.toLowerCase()),
    ]);
    policy.upstreamHeaders.forEach((header, headerIndex) => {
      const name = header.toLowerCase();
      const isForbidden = forbiddenUpstreamHeaders.has(name) || name.startsWith(':');
      if (isForbidden) {
        problems.push({
          path: `${prefix}.external_auths[${index}].upstream_headers[${headerIndex}]`,
          message:
            "host, pseudo headers and hop by hop headers can't be set on the upstream request",
        });
      }
      if (sentToCheck.has(name)) {
        problems.push({
          path: `${prefix}.external_auths[${index}].upstream_headers[${headerIndex}]`,
          message: `external auth ${policy.name}: upstream header ${name} is also sent to the check, it would be removed from the request before the check sees it`,
        });
      }
    });
  });
  const undefinedName = (path: string, name: string) => {
    if (name !== '' && !defined.has(name)) {
      problems.push({ path, message: `external auth ${name} is not defined in external_auths` });
    }
  };
  config.virtualHosts.forEach((vhost, hostIndex) => {
    const hostPath = `${prefix}.virtual_hosts[${hostIndex}]`;
    undefinedName(`${hostPath}.external_auth`, vhost.externalAuth);
    vhost.routes.forEach((route, routeIndex) => {
      const routeName =
        route.externalAuth.case === 'externalAuthName' ? route.externalAuth.value : '';
      undefinedName(`${hostPath}.routes[${routeIndex}].external_auth`, routeName);
    });
  });
  return problems;
}

function vipRules({ api, isAdmin, features }: RulesContext): KindRules {
  // UpdateVIP refuses an ip or prefix_length change to everyone but admins, and an ip change to admins too while the
  // ipam is on
  const readonly = (vip: Resource | null): string[] => {
    const isNew = vip === null;
    if (isNew) {
      return [];
    }
    if (!isAdmin) {
      return ['ip', 'prefix_length'];
    }
    return features.ipamEnabled ? ['ip'] : [];
  };
  if (!features.ipamEnabled) {
    return { readonly };
  }
  const fromIpam: CreateWay = {
    label: 'IP from the ipam',
    greyed: '',
    request: {
      schema: RegisterVipRequestSchema,
      send: async (request, options) => {
        const register = request as RegisterVipRequest;
        if (register.location.case === 'instanceUuid') {
          await refuseLockedInstance(api, register.location.value);
        }
        return (await api.vip.registerVip(register, options)).vip;
      },
    },
  };
  // CreateVIP stays admin only with the ipam on, only RegisterVip is open to everyone
  const explicitIp: CreateWay = {
    label: 'Explicit IP',
    greyed: adminReason('vip', 'create', { ipamEnabled: false }, isAdmin),
    request: null,
  };
  return { readonly, createWays: [fromIpam, explicitIp] };
}

// refuseLockedInstance stops a RegisterVip into a locked instance, the VIP would change that instance. The
// controller never checks the lock.
async function refuseLockedInstance(api: Api, uuid: string) {
  const instance = (await api.instance.readInstance({ UUID: uuid }, silentCall)).instance;
  if (instance === undefined || !isLocked(instance)) {
    return;
  }
  const reason = `The instance ${instance.name} is locked. ${lockReason}`;
  throw new FieldsFailure([{ path: 'instance_uuid', message: reason }], reason);
}

// onCardPath runs one rpc of a saver, its violations under the request field `from` then land on the card path `to`
async function onCardPath<T>(rpc: Promise<T>, from: string, to: string): Promise<T> {
  try {
    return await rpc;
  } catch (err) {
    const connectErr = ConnectError.from(err);
    const errors = violationsOf(connectErr).map((violation) => {
      const path = violation.path;
      const isUnder = path === from || path.startsWith(from + '.') || path.startsWith(from + '[');
      return isUnder ? { ...violation, path: to + path.slice(from.length) } : violation;
    });
    throw new FieldsFailure(errors, connectErr.rawMessage);
  }
}

function instanceRules({ api, dialog }: RulesContext): KindRules {
  return {
    // UpdateInstance keeps the zone and the resource type the instance was created with
    readonly: (instance) => (instance === null ? [] : ['zone_uuid', 'resource_type']),
    // UpdateInstance refuses a change of vips_uuid, a VIP is added or removed with its own rpc
    saveCard: {
      VIPs: (stored, edited) =>
        saveInstanceVips(api, dialog, stored as Instance, edited as Instance),
    },
  };
}

async function saveInstanceVips(
  api: Api,
  dialog: MatDialog,
  stored: Instance,
  edited: Instance,
): Promise<Resource | null> {
  const added = edited.vipsUuid.filter((uuid) => !stored.vipsUuid.includes(uuid));
  const removed = stored.vipsUuid.filter((uuid) => !edited.vipsUuid.includes(uuid));
  let ran = false;
  for (const vipUuid of added) {
    const vip = await vipName(api, vipUuid);
    const agreed = await confirm(dialog, {
      title: `Add the VIP ${vip} to the instance ${stored.name}?`,
      message: `The agents of the instance ${stored.name} then serve the VIP ${vip}.`,
      confirm: 'Add',
    });
    if (!agreed) {
      continue;
    }
    await onCardPath(
      api.instance.addVipToInstance({ UUID: stored.UUID, vipUuid }, silentCall),
      'vip_uuid',
      'vips_uuid',
    );
    ran = true;
  }
  for (const vipUuid of removed) {
    const vip = await vipName(api, vipUuid);
    const agreed = await confirm(dialog, {
      title: `Remove the VIP ${vip} from the instance ${stored.name}?`,
      message: `The agents of the instance ${stored.name} then stop serving the VIP ${vip}.`,
      confirm: 'Remove',
    });
    if (!agreed) {
      continue;
    }
    await onCardPath(
      api.instance.deleteVipFromInstance({ UUID: stored.UUID, vipUuid }, silentCall),
      'vip_uuid',
      'vips_uuid',
    );
    ran = true;
  }
  if (!ran) {
    return null;
  }
  return (await api.instance.readInstance({ UUID: stored.UUID })).instance ?? edited;
}

// vipName names a VIP in a confirmation, a VIP the user can't read keeps its uuid
async function vipName(api: Api, uuid: string): Promise<string> {
  try {
    return (await api.vip.readVIP({ UUID: uuid }, silentCall)).vip?.name ?? uuid;
  } catch {
    return uuid;
  }
}

function zoneRules({ api, dialog }: RulesContext): KindRules {
  return {
    problems: (stored, edited) =>
      poolProblems((stored as Zone | null)?.pools ?? [], (edited as Zone).pools),
    itemGreyed: {
      pools: (pool) => (isLocked(pool as IpPool) ? lockedPoolReason((pool as IpPool).name) : ''),
    },
    // UpdateZone refuses pools other than the stored ones, each pool change has its own rpc
    saveCard: {
      Pools: (stored, edited) => saveZonePools(api, dialog, stored as Zone, edited as Zone),
    },
  };
}

// poolProblems refuses changing or removing a pool flagged with the lock key, which locks that pool only. It also
// refuses the lock key added to a pool, since only an automated owner sets it.
function poolProblems(storedPools: IpPool[], editedPools: IpPool[]): FieldError[] {
  const storedByName = new Map(storedPools.map((pool) => [pool.name, pool]));
  const editedNames = new Set(editedPools.map((pool) => pool.name));
  const problems: FieldError[] = [];
  editedPools.forEach((pool, index) => {
    const before = storedByName.get(pool.name);
    const lockedPoolChanged =
      before !== undefined && isLocked(before) && !equals(IpPoolSchema, before, pool);
    if (lockedPoolChanged) {
      problems.push({ path: `pools[${index}]`, message: lockedPoolReason(pool.name) });
    }
    const hadLockKey = before !== undefined && lockMetadataKey in before.metadata;
    const addsLockKey = lockMetadataKey in pool.metadata && !hadLockKey;
    if (addsLockKey) {
      problems.push({ path: `pools[${index}].metadata`, message: lockKeyRefusal });
    }
  });
  for (const pool of storedPools) {
    const lockedPoolRemoved = isLocked(pool) && !editedNames.has(pool.name);
    if (lockedPoolRemoved) {
      problems.push({ path: 'pools', message: lockedPoolReason(pool.name) });
    }
  }
  return problems;
}

function lockedPoolReason(name: string): string {
  return `The pool ${name} is locked. ${lockReason}`;
}

// saveZonePools sends each pool change with its own rpc and confirmation. A pool is known by its name, so a renamed
// pool is deleted then added again.
async function saveZonePools(
  api: Api,
  dialog: MatDialog,
  stored: Zone,
  edited: Zone,
): Promise<Resource | null> {
  const storedByName = new Map(stored.pools.map((pool) => [pool.name, pool]));
  const editedNames = new Set(edited.pools.map((pool) => pool.name));
  const removed = stored.pools.filter((pool) => !editedNames.has(pool.name));
  const added = edited.pools.filter((pool) => !storedByName.has(pool.name));
  const changed = edited.pools.filter((pool) => {
    const before = storedByName.get(pool.name);
    return before !== undefined && !equals(IpPoolSchema, before, pool);
  });
  const zone = stored.name;
  let saved: Zone | undefined;
  let ran = false;
  for (const pool of removed) {
    const agreed = await confirm(dialog, {
      title: `Delete the pool ${pool.name} of the zone ${zone}?`,
      message: `The pool ${pool.name} is deleted from the zone ${zone}.`,
      confirm: 'Delete',
    });
    if (!agreed) {
      continue;
    }
    const response = await onCardPath(
      api.zone.deletePoolFromZone({ UUID: stored.UUID, poolName: pool.name }, silentCall),
      'pool_name',
      'pools',
    );
    saved = response.zone;
    ran = true;
  }
  for (const pool of changed) {
    const agreed = await confirmChange(dialog, {
      title: `Save the pool ${pool.name}?`,
      message: `Saving stores the pool ${pool.name} of the zone ${zone} with these changes.`,
      changes: fieldChanges(IpPoolSchema, storedByName.get(pool.name)!, pool),
      confirm: 'Save',
    });
    if (!agreed) {
      continue;
    }
    const response = await onCardPath(
      api.zone.updatePoolOfZone({ UUID: stored.UUID, pool }, silentCall),
      'pool',
      `pools[${edited.pools.indexOf(pool)}]`,
    );
    saved = response.zone;
    ran = true;
  }
  for (const pool of added) {
    const agreed = await confirmChange(dialog, {
      title: `Add the pool ${pool.name} to the zone ${zone}?`,
      message: `The zone ${zone} gets the pool ${pool.name}.`,
      changes: fieldChanges(IpPoolSchema, create(IpPoolSchema), pool),
      confirm: 'Add',
    });
    if (!agreed) {
      continue;
    }
    const response = await onCardPath(
      api.zone.addPoolToZone({ UUID: stored.UUID, pool }, silentCall),
      'pool',
      `pools[${edited.pools.indexOf(pool)}]`,
    );
    saved = response.zone;
    ran = true;
  }
  if (!ran) {
    return null;
  }
  return saved ?? edited;
}
