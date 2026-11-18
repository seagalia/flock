import { Agent_Mode } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { Api, silentCall } from '../core/api';
import { kindOf, Resource, resourceId, typeUrlPrefix } from '../core/resources';
import { AgentKinds } from '../forms/proto-form';
import { usedBy } from './used-by';

// instancesServing follows the "used by" links (backend to vs to vip to instance) until it reaches instances. A level
// may mix kinds, so instances are kept aside as they appear and only the rest is expanded.
export async function instancesServing(api: Api, start: Resource[]): Promise<Resource[]> {
  const instances = new Map<string, Resource>();
  let frontier = start;
  while (frontier.length > 0) {
    const toExpand: Resource[] = [];
    for (const one of frontier) {
      const isInstance = kindOf(one).name === 'instance';
      if (isInstance) {
        instances.set(resourceId(one), one);
      } else {
        toExpand.push(one);
      }
    }
    const next = (await Promise.all(toExpand.map((one) => usedBy(api, one)))).flat();
    frontier = [
      ...new Map(next.map((user) => [resourceId(user.resource), user.resource])).values(),
    ];
  }
  return [...instances.values()];
}

// agentKinds tells which extension kinds every agent serving the resource supports, so the Advanced card offers
// only what all of them can unpack. It reads the agents, so the card calls it when it enters edit mode.
export async function agentKinds(api: Api, resource: Resource): Promise<AgentKinds> {
  const instances = await instancesServing(api, [resource]);
  const reported = await Promise.all(
    instances.map(async (instance) => {
      const instanceUuid = resourceId(instance);
      const [agents, { nodes }] = await Promise.all([
        // without the agents no node is known as a provisioner, so every node counts
        api.lookup
          .listAgent({ instanceUuid }, silentCall)
          .then((resp) => resp.agents)
          .catch(() => []),
        api.lookup.listInstanceNodes({ instanceUuid }, silentCall),
      ]);
      // a provisioner unpacks none of the options of the resources it serves no traffic for
      const provisionerUuids = new Set(
        agents.filter((agent) => agent.mode === Agent_Mode.PROVISIONER).map((agent) => agent.UUID),
      );
      const agentNodes = nodes.filter((one) => !provisionerUuids.has(one.agentUuid));
      return { name: instance.name, nodes: agentNodes.map((one) => one.node!).filter(Boolean) };
    }),
  );
  const withNodes = reported.filter((instance) => instance.nodes.length > 0);
  if (withNodes.length === 0) {
    return { kinds: 'any', reason: 'No instance yet' };
  }

  // a disabled extension is present on the agent but can't be used
  const supportedByNode = withNodes
    .flatMap((instance) => instance.nodes)
    .map(
      (node) =>
        new Set(
          node.extensions
            .filter((extension) => !extension.disabled)
            .map((extension) => extension.typeUrl.replace(typeUrlPrefix, '')),
        ),
    );
  const shared = [...supportedByNode[0]].filter((name) =>
    supportedByNode.every((supported) => supported.has(name)),
  );
  if (shared.length > 0) {
    return { kinds: shared, reason: '' };
  }

  const runs = withNodes.map((instance) => {
    const types = [...new Set(instance.nodes.map((node) => node.clientType))];
    return `${instance.name} runs ${types.join(' and ')}`;
  });
  const scope = withNodes.length === 2 ? 'both' : 'all of them';
  return {
    kinds: [],
    reason: `${runs.join(', ')}, no option is shared, so this resource can carry none while it is on ${scope}.`,
  };
}
