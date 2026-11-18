import { create } from '@bufbuild/protobuf';
import { Agent_Mode, AgentSchema } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { InstanceSchema } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api } from '../core/api';
import { agentKinds, instancesServing } from './agent-kinds';

const httpOptions = 'flock.extensions.vs.starling_options.v1.HttpOptions';

function node(clientType: string, ...typeUrls: string[]) {
  return {
    agentUuid: '',
    node: {
      clientType,
      extensions: typeUrls.map((typeUrl) => ({ typeUrl: 'type.googleapis.com/' + typeUrl })),
    },
  };
}

// a provisioner's node, the agent of the uuid is declared in mode PROVISIONER
function provisionerNode(clientType: string, ...typeUrls: string[]) {
  return { ...node(clientType, ...typeUrls), agentUuid: 'p1' };
}

// a vs on one vip, the vip on the given instances, each reporting the given nodes
function fakeApi(nodesByInstance: Record<string, ReturnType<typeof node>[]>) {
  const instances = Object.keys(nodesByInstance).map((name) =>
    create(InstanceSchema, { UUID: name, name }),
  );
  return {
    list: vi.fn(async (kind: string) => {
      if (kind === 'vip') {
        return [create(VIPSchema, { UUID: 'vip1', name: 'vip' })];
      }
      return kind === 'instance' ? instances : [];
    }),
    lookup: {
      listAgent: vi.fn(async () => ({
        agents: [create(AgentSchema, { UUID: 'p1', name: 'acme', mode: Agent_Mode.PROVISIONER })],
      })),
      listInstanceNodes: vi.fn(async ({ instanceUuid }: { instanceUuid: string }) => ({
        nodes: nodesByInstance[instanceUuid],
      })),
    },
  } as unknown as Api;
}

const vs = create(VirtualServerSchema, { UUID: 'vs1', name: 'web' });

describe('agentKinds', () => {
  it('keeps the kinds every node reports', async () => {
    const api = fakeApi({ a: [node('starling', httpOptions)], b: [node('starling', httpOptions)] });

    expect(await agentKinds(api, vs)).toEqual({ kinds: [httpOptions], reason: '' });
  });

  it('is empty with the instances named when two agent types share nothing', async () => {
    const api = fakeApi({
      'prod-lb': [node('starling', httpOptions)],
      'dc2-f5': [node('f5', 'flock.extensions.vs.f5_options.v1.Pool')],
    });

    expect(await agentKinds(api, vs)).toEqual({
      kinds: [],
      reason:
        'prod-lb runs starling, dc2-f5 runs f5, no option is shared, so this resource can carry none while it is on both.',
    });
  });

  it('leaves the extensions of a provisioner out', async () => {
    const api = fakeApi({
      a: [
        node('starling', httpOptions),
        provisionerNode('acme', 'flock.extensions.cert.acme.v1.Acme'),
      ],
    });

    expect(await agentKinds(api, vs)).toEqual({ kinds: [httpOptions], reason: '' });
  });

  it('keeps every node when the agents of an instance cannot be read', async () => {
    const api = fakeApi({ a: [node('starling', httpOptions)] });
    vi.mocked(api.lookup.listAgent).mockRejectedValue(new Error('permission denied'));

    expect(await agentKinds(api, vs)).toEqual({ kinds: [httpOptions], reason: '' });
  });

  it('offers any kind for a vs on no vip', async () => {
    const api = {
      list: vi.fn(async () => []),
      lookup: { listInstanceNodes: vi.fn() },
    } as unknown as Api;

    expect(await agentKinds(api, vs)).toEqual({ kinds: 'any', reason: 'No instance yet' });
  });

  it('reaches the instances of a level that mixes a vip and an instance', async () => {
    const found = create(InstanceSchema, { UUID: 'a', name: 'a' });
    const other = create(InstanceSchema, { UUID: 'b', name: 'b' });
    const api = { list: vi.fn(async () => [other]) } as unknown as Api;
    const vip = create(VIPSchema, { UUID: 'vip1', name: 'vip' });

    const instances = await instancesServing(api, [vip, found]);

    expect(instances.map((one) => one.name).sort()).toEqual(['a', 'b']);
  });
});
