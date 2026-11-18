import { timestampDate } from '@bufbuild/protobuf/wkt';
import { Agent, Agent_Mode } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { State, Status } from '../../gen/flock/api/config/agent/v1/status_pb';
import { Extension, SemanticVersion } from '../../gen/flock/api/config/core/v1/base_pb';
import { VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { GetAgentStatusResponse, InstanceNode } from '../../gen/flock/services/lookup/v1/lookup_pb';
import { agentHealth, HealthLevel } from './display';

export const levelLabels: Record<HealthLevel, string> = {
  ok: 'OK',
  warning: 'Warning',
  error: 'Error',
  unknown: 'No status',
};

// BridgeComponent is one load balancer a bridge agent controls, with the VIPs it announces
export interface BridgeComponent {
  name: string;
  level: HealthLevel;
  vips: VIP[];
}

// AgentView is what the dashboard shows of one declared agent: its declaration, the node it announced when it
// connected and the status it reports
export interface AgentView {
  agent: Agent;
  isBridge: boolean;
  // a provisioner drives no load balancer, it has neither mode nor VIPs to show
  isProvisioner: boolean;
  // null until the agent connects to a controller with a node paired to it
  node: InstanceNode | null;
  version: string;
  lastSeen: Date | null;
  // unknown when the agent gives no status (no show_status feature) or its status is not read yet
  level: HealthLevel;
  // why the agent is not ok: its own error (unreachable included) or the first component not ok
  cause: string;
  components: State[];
  // the VIPs a sidecar agent announces, null when unknown
  vips: VIP[] | null;
  // the load balancers of a bridge agent, empty for a sidecar one
  bridgeComponents: BridgeComponent[];
}

// AgentsReport gathers the agents of an instance apart from its provisioners, and the nodes connected with no declared
// agent to pair with
export interface AgentsReport {
  agents: AgentView[];
  provisioners: AgentView[];
  undeclared: InstanceNode[];
  // the version most agents run, a different one is pointed out
  commonVersion: string;
}

// Announcer is an agent announcing a VIP, through one of its components for a bridge agent
export interface Announcer {
  view: AgentView;
  label: string;
  level: HealthLevel;
}

export function formatVersion(version: SemanticVersion | undefined): string {
  if (!version) {
    return '';
  }
  const prerelease = version.prerelease ? `-${version.prerelease}` : '';
  const metadata = version.metadata ? `+${version.metadata}` : '';
  return `${version.major}.${version.minor}.${version.patch}${prerelease}${metadata}`;
}

export function levelOf(status: Status): HealthLevel {
  switch (status) {
    case Status.OK:
      return 'ok';
    case Status.WARNING:
      return 'warning';
    case Status.ERRORED:
      return 'error';
    default:
      return 'unknown';
  }
}

// agentsReport pairs each declared agent with its node (by the uuid the controller gave the node) and its status, and
// puts the provisioners apart from the load balancer agents
export function agentsReport(
  agents: Agent[],
  statuses: GetAgentStatusResponse[] | null,
  nodes: InstanceNode[],
): AgentsReport {
  const views = agents.map((agent) => {
    const node = nodes.find((one) => one.agentUuid === agent.UUID) ?? null;
    const status = statuses?.find((one) => one.agentUuid === agent.UUID) ?? null;
    return agentView(agent, node, status);
  });
  const declaredUuids = new Set(agents.map((agent) => agent.UUID));
  const loadBalancers = views.filter((view) => !view.isProvisioner);
  return {
    agents: loadBalancers,
    provisioners: views.filter((view) => view.isProvisioner),
    undeclared: nodes.filter((node) => !declaredUuids.has(node.agentUuid)),
    commonVersion: mostCommon(loadBalancers.map((view) => view.version).filter(Boolean)),
  };
}

function agentView(
  agent: Agent,
  node: InstanceNode | null,
  status: GetAgentStatusResponse | null,
): AgentView {
  const view: AgentView = {
    agent,
    isBridge: agent.mode === Agent_Mode.BRIDGE,
    isProvisioner: agent.mode === Agent_Mode.PROVISIONER,
    node,
    version: formatVersion(node?.node?.clientVersion),
    lastSeen: node?.lastSeen ? timestampDate(node.lastSeen) : null,
    level: 'unknown',
    cause: '',
    components: [],
    vips: null,
    bridgeComponents: [],
  };
  const state = status?.agentStatus?.state;
  if (!status || !state?.case) {
    return view;
  }
  view.level = levelOf(agentHealth(status));
  view.components = state.value.componentStates;
  const firstNotOk = view.components.find((component) => component.status !== Status.OK);
  const componentCause = firstNotOk ? `${firstNotOk.name}: ${firstNotOk.details}` : '';
  if (state.case === 'sidecarAgentState') {
    const own = state.value.agentState;
    const ownIsOk = !own || own.status === Status.OK;
    view.cause = ownIsOk ? componentCause : own.details;
    // an agent that could not be reached tells nothing of its VIPs
    view.vips = ownIsOk ? state.value.announcingVips : null;
    return view;
  }
  view.cause = componentCause;
  view.bridgeComponents = view.components.map((component) => ({
    name: component.name,
    level: levelOf(component.status),
    vips:
      state.value.componentAnnouncingVips.find((one) => one.name === component.name)
        ?.announcingVips ?? [],
  }));
  return view;
}

function mostCommon(values: string[]): string {
  const counts = new Map<string, number>();
  for (const value of values) {
    counts.set(value, (counts.get(value) ?? 0) + 1);
  }
  let best = '';
  let bestCount = 0;
  for (const [value, count] of counts) {
    if (count > bestCount) {
      best = value;
      bestCount = count;
    }
  }
  return best;
}

// announcersOf lists who announces the VIP: the sidecar agents holding it, the components of a bridge agent
export function announcersOf(report: AgentsReport, vipUuid: string): Announcer[] {
  const announcers: Announcer[] = [];
  for (const view of report.agents) {
    for (const component of view.bridgeComponents) {
      if (component.vips.some((vip) => vip.UUID === vipUuid)) {
        announcers.push({ view, label: component.name, level: component.level });
      }
    }
    if (view.vips?.some((vip) => vip.UUID === vipUuid)) {
      announcers.push({ view, label: view.agent.name, level: view.level });
    }
  }
  return announcers;
}

// AnnouncerChips names who announces a VIP in the narrow IPs cell of the Overview: the first announcers, then a count
// of the others, so a VIP every agent announces through BGP doesn't widen the table.
export interface AnnouncerChips {
  shown: Announcer[];
  others: Announcer[];
  // the number of agents when every agent of the instance announces the VIP, which one chip then says, else 0
  allAgents: number;
  // the worst state among the others, for the dot of their chip
  othersLevel: HealthLevel;
}

const namedAnnouncers = 2;
const levelsByGravity: HealthLevel[] = ['ok', 'unknown', 'warning', 'error'];

export function announcerChips(report: AgentsReport, vipUuid: string): AnnouncerChips {
  const announcers = announcersOf(report, vipUuid);
  // a sidecar agent announces a VIP once, so as many sidecar announcers as agents is all of them
  const allSidecars = announcers.every((one) => !one.view.isBridge);
  const isEveryAgent =
    allSidecars &&
    announcers.length > namedAnnouncers &&
    announcers.length === report.agents.length;
  const shown = isEveryAgent ? [] : announcers.slice(0, namedAnnouncers);
  const others = isEveryAgent ? announcers : announcers.slice(namedAnnouncers);
  const othersLevel = others.reduce<HealthLevel>(
    (worst, one) =>
      levelsByGravity.indexOf(one.level) > levelsByGravity.indexOf(worst) ? one.level : worst,
    'ok',
  );
  return { shown, others, allAgents: isEveryAgent ? announcers.length : 0, othersLevel };
}

// someAgentUnreachable tells a VIP with no announcer may still be held by an agent the controller can't read
export function someAgentUnreachable(report: AgentsReport): boolean {
  return report.agents.some((view) => {
    const gaveStatus = view.level !== 'unknown';
    const vipsUnknown = !view.isBridge && view.vips === null;
    return gaveStatus && vipsUnknown;
  });
}

// isVersionDrift points out an agent running another version than most agents of the instance
export function isVersionDrift(report: AgentsReport, view: AgentView): boolean {
  const bothKnown = view.version !== '' && report.commonVersion !== '';
  return bothKnown && view.version !== report.commonVersion;
}

// ExtensionGroup is the extensions of one Any field: `vip.typed_announce` with `starling_announce.Bgp`, ...
export interface ExtensionGroup {
  category: string;
  extensions: { name: string; typeUrl: string; disabled: boolean }[];
}

// extensionGroups groups the extensions a node reports by the field they are packed into, with short names
export function extensionGroups(extensions: Extension[]): ExtensionGroup[] {
  const groups = new Map<string, ExtensionGroup>();
  for (const extension of extensions) {
    const category = extension.category.split('.').slice(-2).join('.');
    const group = groups.get(category) ?? { category, extensions: [] };
    group.extensions.push({
      name: shortExtensionName(extension.typeUrl),
      typeUrl: extension.typeUrl,
      disabled: extension.disabled,
    });
    groups.set(category, group);
  }
  return [...groups.values()].sort((a, b) => a.category.localeCompare(b.category));
}

// type.googleapis.com/flock.extensions.vip.starling_announce.v1.Bgp is starling_announce.Bgp
function shortExtensionName(typeUrl: string): string {
  const parts = typeUrl.split('/').pop()!.split('.');
  const message = parts[parts.length - 1];
  const pkg = parts.length >= 3 ? parts[parts.length - 3] : '';
  return pkg ? `${pkg}.${message}` : message;
}

// "3 min ago", "5 days ago"
export function timeAgo(date: Date, now = new Date()): string {
  const seconds = Math.max(0, Math.round((now.getTime() - date.getTime()) / 1000));
  if (seconds < 60) {
    return 'just now';
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes} min ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours} h ago`;
  }
  const days = Math.floor(hours / 24);
  return days === 1 ? '1 day ago' : `${days} days ago`;
}
