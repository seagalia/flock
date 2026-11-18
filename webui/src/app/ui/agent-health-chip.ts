import { Component, effect, inject, input, signal, untracked } from '@angular/core';
import { Code, ConnectError } from '@connectrpc/connect';
import { Agent_Mode } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { Instance } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { agentHealth, HealthLevel, healthLevel } from '../core/display';
import { levelOf } from '../core/agents';
import { Api, silentCall } from '../core/api';

interface Summary {
  text: string;
  level: HealthLevel;
}

// AgentHealthChip is the "2 / 3 healthy" of an instance in the instances list. It reads the status of the agents
// once, quietly: an instance whose agents give no status shows it rather than an error.
@Component({
  selector: 'flock-agent-health-chip',
  template: `
    @if (summary(); as summary) {
      <span [class]="'chip ' + summary.level">{{ summary.text }}</span>
    } @else {
      <span class="chip unknown">Loading ...</span>
    }
  `,
})
export class AgentHealthChip {
  readonly instance = input.required<Instance>();
  private api = inject(Api);
  protected summary = signal<Summary | null>(null);

  constructor() {
    effect(() => {
      const instanceUuid = this.instance().UUID;
      untracked(() => this.load(instanceUuid));
    });
  }

  private async load(instanceUuid: string) {
    try {
      // listAgent only reads the controller config: an instance with no agent skips the status call. A provisioner
      // drives no load balancer, so it counts in neither side of the chip.
      const { agents } = await this.api.lookup.listAgent({ instanceUuid }, silentCall);
      const agentUuids = new Set(
        agents.filter((agent) => agent.mode !== Agent_Mode.PROVISIONER).map((agent) => agent.UUID),
      );
      if (agentUuids.size === 0) {
        this.summary.set({ text: 'No agent', level: 'unknown' });
        return;
      }
      const { agentStatus } = await this.api.lookup.listAgentStatus({ instanceUuid }, silentCall);
      const ofAgents = agentStatus.filter((status) => agentUuids.has(status.agentUuid));
      const healthy = ofAgents.filter((status) => levelOf(agentHealth(status)) === 'ok').length;
      this.summary.set({
        text: `${healthy} / ${ofAgents.length} healthy`,
        level: healthLevel(healthy, ofAgents.length),
      });
    } catch (err) {
      // the controller answers Unimplemented when no agent of the instance gives its status
      const noStatus = err instanceof ConnectError && err.code === Code.Unimplemented;
      this.summary.set({ text: noStatus ? 'No status' : 'Unknown', level: 'unknown' });
    }
  }
}
