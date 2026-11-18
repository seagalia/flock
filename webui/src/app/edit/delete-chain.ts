import { ConnectError } from '@connectrpc/connect';
import { Backend } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { Role } from '../../gen/flock/api/config/permission/v1/permission_pb';
import { VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api, silentCall } from '../core/api';
import { vsBackendUuids } from '../core/display';
import { Access, RoleCache } from './access';
import { isLocked, lockReason } from './lock';
import { usedBy } from './used-by';

// A backend of the virtual server that stays, with why
export interface KeptBackend {
  name: string;
  reason: string;
}

// ChainPlan is what deleting a virtual server with its chain does, the chain the config map shows. The VIPs listing
// it are updated, never deleted, since they may serve other virtual servers.
export interface ChainPlan {
  vs: VirtualServer;
  vips: VIP[];
  backends: Backend[];
  kept: KeptBackend[];
  // why nothing can be done, empty when the plan can run
  refusal: string;
}

export interface ChainOutcome {
  done: string[];
  notDone: string[];
  error: string;
}

// planChainDeletion reads the chain and checks it on click. A missing role or a lock on the virtual server or one of
// its VIPs refuses the whole plan. A backend another virtual server uses, or one the user can't delete, is kept.
export async function planChainDeletion(
  api: Api,
  access: Access,
  vs: VirtualServer,
): Promise<ChainPlan> {
  const plan: ChainPlan = { vs, vips: [], backends: [], kept: [], refusal: '' };
  const cache: RoleCache = new Map();
  if (isLocked(vs)) {
    return { ...plan, refusal: lockReason };
  }
  const vsDenial = await access.check(vs, Role.OWNER, cache);
  if (vsDenial) {
    return { ...plan, refusal: vsDenial.reason };
  }

  plan.vips = (await usedBy(api, vs, ['vip'])).map((user) => user.resource as VIP);
  for (const vip of plan.vips) {
    if (isLocked(vip)) {
      return { ...plan, refusal: `VIP ${vip.name}: ${lockReason}` };
    }
    const vipDenial = await access.check(vip, Role.WRITER, cache, 'Removing a virtual server');
    if (vipDenial) {
      return { ...plan, refusal: vipDenial.reason };
    }
  }

  for (const uuid of vsBackendUuids(vs)) {
    let backend: Backend | undefined;
    try {
      backend = (await api.backend.readBackend({ UUID: uuid }, silentCall)).backend;
    } catch {
      // a backend the user can't read is not theirs to delete
    }
    if (!backend) {
      plan.kept.push({ name: uuid, reason: "you can't read it" });
      continue;
    }
    const otherUsers = (await usedBy(api, backend, ['vs'])).filter(
      (user) => user.resource.UUID !== vs.UUID,
    );
    if (otherUsers.length > 0) {
      const names = otherUsers.map((user) => user.resource.name).join(', ');
      plan.kept.push({ name: backend.name, reason: `used by ${names}` });
      continue;
    }
    if (isLocked(backend)) {
      plan.kept.push({ name: backend.name, reason: 'locked by flock.autoprovision' });
      continue;
    }
    const backendDenial = await access.check(backend, Role.OWNER, cache);
    if (backendDenial) {
      plan.kept.push({ name: backend.name, reason: "you don't own it" });
      continue;
    }
    plan.backends.push(backend);
  }
  return plan;
}

// chainSteps names what deleteChain does, in the order it does it, for the confirmation
export function chainSteps(plan: ChainPlan): string[] {
  return [
    ...plan.vips.map((vip) => `remove ${plan.vs.name} from the VIP ${vip.name} (${vip.ip})`),
    `delete the virtual server ${plan.vs.name}`,
    ...plan.backends.map((backend) => `delete the backend ${backend.name}`),
  ];
}

// deleteChain runs the plan in the order the controller accepts: a virtual server a VIP lists can't be deleted, nor a
// backend a virtual server uses. It stops at the first error and nothing done is rolled back.
export async function deleteChain(api: Api, plan: ChainPlan): Promise<ChainOutcome> {
  const steps = chainSteps(plan);
  const actions: (() => Promise<unknown>)[] = [
    ...plan.vips.map((vip) => async () => {
      // read again, so a virtual server added to the VIP since the plan is kept
      const stored = (await api.vip.readVIP({ UUID: vip.UUID }, silentCall)).vip ?? vip;
      const virtualServersUuid = stored.virtualServersUuid.filter((uuid) => uuid !== plan.vs.UUID);
      await api.vip.updateVIP(
        { UUID: vip.UUID, vip: { ...stored, virtualServersUuid } },
        silentCall,
      );
    }),
    () => api.vs.deleteVS({ UUID: plan.vs.UUID }, silentCall),
    ...plan.backends.map(
      (backend) => () => api.backend.deleteBackend({ UUID: backend.UUID }, silentCall),
    ),
  ];
  const outcome: ChainOutcome = { done: [], notDone: [], error: '' };
  for (const [index, action] of actions.entries()) {
    try {
      await action();
    } catch (err) {
      outcome.error = ConnectError.from(err).rawMessage;
      outcome.notDone = steps.slice(index);
      return outcome;
    }
    outcome.done.push(steps[index]);
  }
  return outcome;
}
