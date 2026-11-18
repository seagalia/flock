import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { create } from '@bufbuild/protobuf';
import { Agent, Agent_Mode, AgentSchema } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { Status } from '../../gen/flock/api/config/agent/v1/status_pb';
import { InstanceSchema } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { GetAgentStatusResponseSchema } from '../../gen/flock/services/lookup/v1/lookup_pb';
import { Api } from '../core/api';
import { AgentHealthChip } from './agent-health-chip';

@Component({
  imports: [AgentHealthChip],
  template: `<flock-agent-health-chip [instance]="instance" />`,
})
class Host {
  instance = create(InstanceSchema, { UUID: 'inst-1', name: 'prod' });
}

const agent = (UUID: string, mode = Agent_Mode.SIDECAR) =>
  create(AgentSchema, { UUID, name: UUID, mode });

const okStatus = (agentUuid: string) =>
  create(GetAgentStatusResponseSchema, {
    agentUuid,
    agentStatus: {
      state: {
        case: 'sidecarAgentState',
        value: { agentState: { name: 'agent', status: Status.OK } },
      },
    },
  });

async function render(agents: Agent[]): Promise<ComponentFixture<Host>> {
  TestBed.configureTestingModule({
    providers: [
      {
        provide: Api,
        useValue: {
          lookup: {
            listAgent: async () => ({ agents }),
            listAgentStatus: async () => ({ agentStatus: agents.map((one) => okStatus(one.UUID)) }),
          },
        },
      },
    ],
  });
  const fixture = TestBed.createComponent(Host);
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
  return fixture;
}

describe('AgentHealthChip', () => {
  it('says No agent for an instance holding only a provisioner', async () => {
    const fixture = await render([agent('p1', Agent_Mode.PROVISIONER)]);

    expect(fixture.nativeElement.textContent).toContain('No agent');
  });

  it('counts the agents only', async () => {
    const fixture = await render([agent('a1'), agent('p1', Agent_Mode.PROVISIONER)]);

    expect(fixture.nativeElement.textContent).toContain('1 / 1 healthy');
  });
});
