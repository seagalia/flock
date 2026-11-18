import { create } from '@bufbuild/protobuf';
import { ConnectError } from '@connectrpc/connect';
import { Backend, BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { Role } from '../../gen/flock/api/config/permission/v1/permission_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api } from '../core/api';
import { KindName, Resource } from '../core/resources';
import { Access, Denial } from './access';
import { chainSteps, deleteChain, planChainDeletion } from './delete-chain';
import { lockReason } from './lock';

const vs = create(VirtualServerSchema, {
  UUID: 'v1',
  name: 'web',
  routeConfig: { case: 'tcpRouteConfig', value: { backendUuid: 'b1' } },
});
const otherVs = create(VirtualServerSchema, { UUID: 'v2', name: 'admin' });
const vip = create(VIPSchema, {
  UUID: 'ip1',
  name: 'front',
  ip: '10.0.0.1',
  virtualServersUuid: ['v1', 'v2'],
});
const backend = create(BackendSchema, { UUID: 'b1', name: 'web-pool' });

// fakeApi lists the vip as user of the virtual server, and the given users for the backend
function fakeApi(backendUsers: Resource[] = [vs], stored: Backend | undefined = backend) {
  const list = vi.fn(async (kind: KindName) => (kind === 'vip' ? [vip] : backendUsers));
  return {
    list,
    vip: {
      readVIP: vi.fn(async () => ({ vip })),
      updateVIP: vi.fn(async () => ({})),
    },
    vs: { deleteVS: vi.fn(async () => ({})) },
    backend: {
      readBackend: vi.fn(async () => ({ backend: stored })),
      deleteBackend: vi.fn(async () => ({})),
    },
  };
}

function fakeAccess(denied: Record<string, string> = {}) {
  const check = vi.fn<(resource: Resource, role: Role) => Promise<Denial | null>>(
    async (resource) => {
      const reason = denied[resource.UUID ?? ''];
      return reason ? { reason } : null;
    },
  );
  return { check } as unknown as Access & { check: typeof check };
}

describe('planChainDeletion', () => {
  it('updates the vips and deletes the virtual server with the backend only it uses', async () => {
    const access = fakeAccess();
    const plan = await planChainDeletion(fakeApi() as unknown as Api, access, vs);

    expect(plan.refusal).toBe('');
    expect(plan.vips).toEqual([vip]);
    expect(plan.backends).toEqual([backend]);
    expect(plan.kept).toEqual([]);
    expect(access.check.mock.calls.map((call) => [call[0].UUID, call[1]])).toEqual([
      ['v1', Role.OWNER],
      ['ip1', Role.WRITER],
      ['b1', Role.OWNER],
    ]);
    expect(chainSteps(plan)).toEqual([
      'remove web from the VIP front (10.0.0.1)',
      'delete the virtual server web',
      'delete the backend web-pool',
    ]);
  });

  it('keeps a backend another virtual server uses', async () => {
    const api = fakeApi([vs, otherVs]);
    const plan = await planChainDeletion(api as unknown as Api, fakeAccess(), vs);

    expect(plan.backends).toEqual([]);
    expect(plan.kept).toEqual([{ name: 'web-pool', reason: 'used by admin' }]);
  });

  it('keeps a backend the user does not own or can not read', async () => {
    const notOwned = await planChainDeletion(
      fakeApi() as unknown as Api,
      fakeAccess({ b1: 'You are a reader of web-pool.' }),
      vs,
    );
    expect(notOwned.kept).toEqual([{ name: 'web-pool', reason: "you don't own it" }]);

    const api = fakeApi();
    api.backend.readBackend.mockRejectedValue(new ConnectError('denied'));
    const unreadable = await planChainDeletion(api as unknown as Api, fakeAccess(), vs);
    expect(unreadable.kept).toEqual([{ name: 'b1', reason: "you can't read it" }]);
  });

  it('refuses the whole chain for a vip the user can not edit', async () => {
    const plan = await planChainDeletion(
      fakeApi() as unknown as Api,
      fakeAccess({ ip1: 'You are a reader of front.' }),
      vs,
    );
    expect(plan.refusal).toBe('You are a reader of front.');
  });

  it('refuses a locked virtual server before any call', async () => {
    const api = fakeApi();
    const locked = create(VirtualServerSchema, {
      ...vs,
      metadata: { 'flock.autoprovision': 'true' },
    });
    const plan = await planChainDeletion(api as unknown as Api, fakeAccess(), locked);

    expect(plan.refusal).toBe(lockReason);
    expect(api.list).not.toHaveBeenCalled();
  });
});

describe('deleteChain', () => {
  it('removes the virtual server from the vip, then deletes it and its backend', async () => {
    const api = fakeApi();
    const plan = await planChainDeletion(api as unknown as Api, fakeAccess(), vs);

    const outcome = await deleteChain(api as unknown as Api, plan);

    expect(outcome).toEqual({ done: chainSteps(plan), notDone: [], error: '' });
    expect(api.vip.updateVIP).toHaveBeenCalledWith(
      expect.objectContaining({
        UUID: 'ip1',
        vip: expect.objectContaining({ virtualServersUuid: ['v2'] }),
      }),
      expect.anything(),
    );
    expect(api.vs.deleteVS).toHaveBeenCalledWith({ UUID: 'v1' }, expect.anything());
    expect(api.backend.deleteBackend).toHaveBeenCalledWith({ UUID: 'b1' }, expect.anything());
  });

  it('stops at the first error and tells what was done and what was not', async () => {
    const api = fakeApi();
    api.vs.deleteVS.mockRejectedValue(new ConnectError('still used'));
    const plan = await planChainDeletion(api as unknown as Api, fakeAccess(), vs);

    const outcome = await deleteChain(api as unknown as Api, plan);

    expect(outcome).toEqual({
      done: ['remove web from the VIP front (10.0.0.1)'],
      notDone: ['delete the virtual server web', 'delete the backend web-pool'],
      error: 'still used',
    });
    expect(api.backend.deleteBackend).not.toHaveBeenCalled();
  });
});
