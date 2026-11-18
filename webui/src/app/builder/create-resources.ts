import { create } from '@bufbuild/protobuf';
import { ConnectError } from '@connectrpc/connect';
import { VIP, VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { RegisterVipRequestSchema } from '../../gen/flock/services/vip/v1/vip_pb';
import { Api, silentCall } from '../core/api';
import { isLocked, lockReason } from '../edit/lock';
import { CreateRequest } from '../edit/resource-panel';
import { buildDocuments, type NewVipInput, type WizardInput } from './documents';

export type VipPlan =
  | { kind: 'none' }
  | { kind: 'existing'; vip: VIP }
  | { kind: 'explicit'; vip: NewVipInput }
  | { kind: 'ipam'; vip: NewVipInput; register: CreateRequest };

export interface CreationPlan {
  input: WizardInput;
  // uuid of the existing backend the run picked, empty when it creates one
  backendUuid: string;
  certificateUuid: string;
  vip: VipPlan;
}

export interface CreationOutcome {
  created: string[];
  notCreated: string[];
  error: string;
}

export interface CreationAction {
  label: string;
  run(): Promise<void>;
}

// creationActions are the rpcs of a direct creation in the order flock apply would send them, each one with the uuids
// the earlier ones got. Their labels are also what the review lists before Create.
export function creationActions(api: Api, plan: CreationPlan): CreationAction[] {
  const { input } = plan;
  let backendUuid = plan.backendUuid;
  let vsUuid = '';
  const refs = () => ({ backend: backendUuid, certificate: plan.certificateUuid });
  const actions: CreationAction[] = [];

  if ('create' in input.backend) {
    actions.push({
      label: `backend ${input.backend.create.name.trim()}`,
      run: async () => {
        const backend = buildDocuments(input, refs()).backend;
        const created = await api.backend.createBackend({ backend }, silentCall);
        backendUuid = created.backend?.UUID ?? '';
      },
    });
  }
  actions.push({
    label: `virtual server ${input.vs.name.trim()}`,
    run: async () => {
      const vs = buildDocuments(input, refs()).vs;
      vsUuid = (await api.vs.createVS({ vs }, silentCall)).vs?.UUID ?? '';
    },
  });
  actions.push(...vipActions(api, plan.vip, () => vsUuid));
  return actions;
}

// runActions runs the actions in order. Like flock apply it stops at the first error and tells what was created and
// what was not.
export async function runActions(actions: CreationAction[]): Promise<CreationOutcome> {
  const created: string[] = [];
  for (const [index, action] of actions.entries()) {
    try {
      await action.run();
    } catch (err) {
      const notCreated = actions.slice(index).map((next) => next.label);
      return { created, notCreated, error: `${action.label}: ${errorMessage(err)}` };
    }
    created.push(action.label);
  }
  return { created, notCreated: [], error: '' };
}

// vipActions give the virtual server to its VIP. RegisterVip takes no virtual server, so a VIP from the ipam gets it
// with an update once registered.
function vipActions(api: Api, plan: VipPlan, vsUuid: () => string): CreationAction[] {
  switch (plan.kind) {
    case 'none':
      return [];
    case 'existing':
      return [addToVip(api, () => plan.vip.UUID, plan.vip.name, vsUuid)];
    case 'explicit': {
      const name = plan.vip.name.trim();
      const createVip = async () => {
        const vip = create(VIPSchema, {
          name,
          ip: plan.vip.ip.trim(),
          virtualServersUuid: [vsUuid()],
        });
        await api.vip.createVIP({ vip }, silentCall);
      };
      return [{ label: `VIP ${name}`, run: createVip }];
    }
    case 'ipam': {
      const name = plan.vip.name.trim();
      let registeredUuid = '';
      const register = async () => {
        const registered = await plan.register.send(registerRequest(plan.vip), silentCall);
        registeredUuid = (registered as VIP | undefined)?.UUID ?? '';
      };
      return [
        { label: `VIP ${name}`, run: register },
        addToVip(api, () => registeredUuid, name, vsUuid),
      ];
    }
  }
}

// addToVip reads the VIP again before its update, so a change made since the wizard listed it is kept
function addToVip(
  api: Api,
  vipUuid: () => string,
  name: string,
  vsUuid: () => string,
): CreationAction {
  return {
    label: `VIP ${name} (virtual server added)`,
    run: async () => {
      const UUID = vipUuid();
      const stored = (await api.vip.readVIP({ UUID }, silentCall)).vip;
      if (!stored) {
        throw new Error(`the VIP ${name} is gone`);
      }
      // the VIP may have been locked since the wizard listed it, and the dashboard never changes a locked VIP
      if (isLocked(stored)) {
        throw new Error(`The VIP ${stored.name} is locked. ${lockReason}`);
      }
      const vip = { ...stored, virtualServersUuid: [...stored.virtualServersUuid, vsUuid()] };
      await api.vip.updateVIP({ UUID, vip }, silentCall);
    },
  };
}

function registerRequest(vip: NewVipInput) {
  const pool = vip.pool.trim();
  const onInstance = vip.target === 'instance';
  return create(RegisterVipRequestSchema, {
    name: vip.name.trim(),
    location: onInstance
      ? { case: 'instanceUuid', value: vip.instanceUuid }
      : { case: 'zoneUuid', value: vip.zoneUuid },
    pool: pool === '' ? undefined : { case: 'poolName', value: pool },
  });
}

// errorMessage is the message of an error, without the code prefix connect gives its own
export function errorMessage(err: unknown): string {
  if (err instanceof ConnectError) {
    return err.rawMessage;
  }
  return err instanceof Error ? err.message : String(err);
}
