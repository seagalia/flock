import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import { Agent_Mode, AgentSchema } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { Status } from '../../gen/flock/api/config/agent/v1/status_pb';
import { ExtensionSchema } from '../../gen/flock/api/config/core/v1/base_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import {
  GetAgentStatusResponseSchema,
  InstanceNodeSchema,
} from '../../gen/flock/services/lookup/v1/lookup_pb';
import {
  agentsReport,
  announcerChips,
  announcersOf,
  extensionGroups,
  formatVersion,
  someAgentUnreachable,
  timeAgo,
} from './agents';

const agent = (UUID: string, name: string, mode = Agent_Mode.SIDECAR) =>
  create(AgentSchema, { UUID, name, host: `${name}:8443`, type: 'starling', mode });

const node = (name: string, uuid = '', minor = 8) =>
  create(InstanceNodeSchema, {
    name,
    agentUuid: uuid,
    node: { clientVersion: { major: 1, minor, patch: 2 } },
    lastSeen: timestampFromDate(new Date('2026-11-03T10:00:00Z')),
  });

const vip = (UUID: string) => create(VIPSchema, { UUID, name: UUID });

const sidecarStatus = (
  agentUuid: string,
  own: Status,
  details: string,
  components: [string, Status, string][],
  vipUuids: string[] = [],
) =>
  create(GetAgentStatusResponseSchema, {
    agentUuid,
    agentStatus: {
      state: {
        case: 'sidecarAgentState',
        value: {
          agentState: { name: 'agent', status: own, details },
          componentStates: components.map(([name, status, componentDetails]) => ({
            name,
            status,
            details: componentDetails,
          })),
          announcingVips: vipUuids.map(vip),
        },
      },
    },
  });

describe('agents report', () => {
  it('pairs a node by the uuid the controller gave it', () => {
    const report = agentsReport([agent('a1', 'lb-1'), agent('a2', 'lb-2')], null, [
      node('lb-1', 'a1'),
      node('lb-2', 'a2'),
    ]);

    expect(report.agents[0].node?.name).toBe('lb-1');
    expect(report.agents[1].node?.name).toBe('lb-2');
    expect(report.undeclared).toEqual([]);
  });

  it('leaves undeclared a node whose uuid no agent has', () => {
    const report = agentsReport([agent('a1', 'lb-1')], null, [node('lb-1', 'unknown-uuid')]);

    expect(report.agents[0].node).toBeNull();
    expect(report.undeclared.map((one) => one.name)).toEqual(['lb-1']);
  });

  it('puts a provisioner apart from the agents, paired with its node', () => {
    const report = agentsReport(
      [agent('a1', 'lb-1'), agent('p1', 'acme', Agent_Mode.PROVISIONER)],
      null,
      [node('lb-1', 'a1'), node('acme', 'p1')],
    );

    expect(report.agents.map((view) => view.agent.name)).toEqual(['lb-1']);
    expect(report.provisioners.map((view) => view.agent.name)).toEqual(['acme']);
    expect(report.provisioners[0].node?.name).toBe('acme');
    expect(report.undeclared).toEqual([]);
  });

  it('gives the version most agents run', () => {
    const report = agentsReport(
      [agent('a1', 'lb-1'), agent('a2', 'lb-2'), agent('a3', 'lb-3')],
      null,
      [node('lb-1', 'a1'), node('lb-2', 'a2'), node('lb-3', 'a3', 7)],
    );

    expect(report.commonVersion).toBe('1.8.2');
    expect(report.agents[2].version).toBe('1.7.2');
  });

  it('leaves an agent with no status unknown', () => {
    const [view] = agentsReport([agent('a1', 'lb-1')], [], []).agents;

    expect(view.level).toBe('unknown');
    expect(view.vips).toBeNull();
  });

  it('gives the first component not ok as the cause', () => {
    const status = sidecarStatus('a1', Status.OK, '', [
      ['envoy', Status.WARNING, '2 clusters warming'],
      ['ipvs', Status.OK, ''],
    ]);

    const [view] = agentsReport([agent('a1', 'lb-1')], [status], []).agents;

    expect(view.level).toBe('warning');
    expect(view.cause).toBe('envoy: 2 clusters warming');
  });

  it('gives the error of an unreachable agent and no VIPs', () => {
    const status = sidecarStatus('a1', Status.ERRORED, 'connection refused', []);

    const report = agentsReport([agent('a1', 'lb-1')], [status], []);

    expect(report.agents[0].level).toBe('error');
    expect(report.agents[0].cause).toBe('connection refused');
    expect(report.agents[0].vips).toBeNull();
    expect(someAgentUnreachable(report)).toBe(true);
  });

  it('finds the agents announcing a VIP, through the components of a bridge agent', () => {
    const sidecar = sidecarStatus('a1', Status.OK, '', [], ['vip-1']);
    const bridge = create(GetAgentStatusResponseSchema, {
      agentUuid: 'a2',
      agentStatus: {
        state: {
          case: 'proxyAgentState',
          value: {
            componentStates: [{ name: 'bigip-a', status: Status.OK }],
            componentAnnouncingVips: [{ name: 'bigip-a', announcingVips: [vip('vip-2')] }],
          },
        },
      },
    });

    const report = agentsReport(
      [agent('a1', 'lb-1'), agent('a2', 'f5', Agent_Mode.BRIDGE)],
      [sidecar, bridge],
      [],
    );

    expect(announcersOf(report, 'vip-1').map((one) => one.label)).toEqual(['lb-1']);
    expect(announcersOf(report, 'vip-2').map((one) => one.label)).toEqual(['bigip-a']);
    expect(announcersOf(report, 'vip-3')).toEqual([]);
    expect(someAgentUnreachable(report)).toBe(false);
  });
});

describe('announcerChips', () => {
  // the first agent's envoy has the state given, an agent gives its VIPs only while its own state is ok
  const sidecars = (count: number, announcing: number, envoyOfFirst = Status.OK) => {
    const agents = [];
    const statuses = [];
    for (let i = 1; i <= count; i++) {
      agents.push(agent(`a${i}`, `lb-${i}`));
      const vipUuids = i <= announcing ? ['vip-1'] : [];
      const envoy = i === 1 ? envoyOfFirst : Status.OK;
      statuses.push(sidecarStatus(`a${i}`, Status.OK, '', [['envoy', envoy, '']], vipUuids));
    }
    return agentsReport(agents, statuses, []);
  };
  const labels = (announcers: { label: string }[]) => announcers.map((one) => one.label);

  it('names up to two announcers', () => {
    const chips = announcerChips(sidecars(3, 2), 'vip-1');

    expect(labels(chips.shown)).toEqual(['lb-1', 'lb-2']);
    expect(chips.others).toEqual([]);
    expect(chips.allAgents).toBe(0);
  });

  it('counts the announcers past the first two', () => {
    const chips = announcerChips(sidecars(6, 5), 'vip-1');

    expect(labels(chips.shown)).toEqual(['lb-1', 'lb-2']);
    expect(labels(chips.others)).toEqual(['lb-3', 'lb-4', 'lb-5']);
    expect(chips.allAgents).toBe(0);
  });

  it('gives one count when every agent of the instance announces the VIP', () => {
    const chips = announcerChips(sidecars(4, 4), 'vip-1');

    expect(chips.shown).toEqual([]);
    expect(labels(chips.others)).toEqual(['lb-1', 'lb-2', 'lb-3', 'lb-4']);
    expect(chips.allAgents).toBe(4);
  });

  it('names both agents of an instance of two', () => {
    const chips = announcerChips(sidecars(2, 2), 'vip-1');

    expect(labels(chips.shown)).toEqual(['lb-1', 'lb-2']);
    expect(chips.allAgents).toBe(0);
  });

  it('gives the worst state of the announcers it counts', () => {
    expect(announcerChips(sidecars(4, 4, Status.WARNING), 'vip-1').othersLevel).toBe('warning');
    expect(announcerChips(sidecars(4, 4), 'vip-1').othersLevel).toBe('ok');
  });
});

describe('agent display helpers', () => {
  it('formats a semantic version', () => {
    expect(
      formatVersion({ major: 1, minor: 2, patch: 3, prerelease: 'rc1', metadata: '' } as never),
    ).toBe('1.2.3-rc1');
    expect(formatVersion(undefined)).toBe('');
  });

  it('groups extensions by the field they go into, with short names', () => {
    const groups = extensionGroups([
      create(ExtensionSchema, {
        typeUrl: 'type.googleapis.com/flock.extensions.vip.starling_announce.v1.Bgp',
        category: 'flock.api.config.vip.v1.vip.typed_announce',
      }),
      create(ExtensionSchema, {
        typeUrl: 'type.googleapis.com/flock.extensions.vs.starling_options.v1.Options',
        category: 'flock.api.config.vs.v1.virtual_server.typed_options',
        disabled: true,
      }),
    ]);

    expect(groups.map((group) => group.category)).toEqual([
      'vip.typed_announce',
      'virtual_server.typed_options',
    ]);
    expect(groups[0].extensions[0].name).toBe('starling_announce.Bgp');
    expect(groups[1].extensions[0].disabled).toBe(true);
  });

  it('tells how long ago', () => {
    const now = new Date('2026-11-03T10:00:00Z');
    expect(timeAgo(new Date('2026-11-03T09:59:30Z'), now)).toBe('just now');
    expect(timeAgo(new Date('2026-11-03T09:57:00Z'), now)).toBe('3 min ago');
    expect(timeAgo(new Date('2026-11-03T08:00:00Z'), now)).toBe('2 h ago');
    expect(timeAgo(new Date('2026-10-29T10:00:00Z'), now)).toBe('5 days ago');
  });
});
