import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { Agent_Mode, AgentSchema } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { Status } from '../../gen/flock/api/config/agent/v1/status_pb';
import { InstanceSchema } from '../../gen/flock/api/config/instance/v1/instance_pb';
import {
  GetAgentStatusResponseSchema,
  InstanceNodeSchema,
} from '../../gen/flock/services/lookup/v1/lookup_pb';
import { Api } from '../core/api';
import { AgentsTable } from './agents-table';

@Component({
  imports: [AgentsTable],
  template: `<flock-agents-table [instance]="instance" />`,
})
class Host {
  instance = create(InstanceSchema, { UUID: 'inst-1', name: 'prod' });
}

const agent = (UUID: string, name: string, mode = Agent_Mode.SIDECAR) =>
  create(AgentSchema, { UUID, name, host: `${name}:8443`, mode, features: { showStatus: true } });

interface Answers {
  agents: ReturnType<typeof agent>[];
  statusFails?: boolean;
  // the url the page is opened on, before the table is created
  url?: string;
}

async function render(answers: Answers): Promise<ComponentFixture<Host>> {
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      {
        provide: Api,
        useValue: {
          lookup: {
            listAgent: async () => ({ agents: answers.agents }),
            listAgentStatus: async () => {
              if (answers.statusFails) {
                throw new Error('unreachable');
              }
              return {
                agentStatus: [
                  create(GetAgentStatusResponseSchema, {
                    agentUuid: 'a1',
                    agentStatus: {
                      state: {
                        case: 'sidecarAgentState',
                        value: {
                          agentState: { name: 'agent', status: Status.OK },
                          componentStates: [
                            {
                              name: 'envoy',
                              status: Status.WARNING,
                              details: '2 clusters warming',
                            },
                          ],
                        },
                      },
                    },
                  }),
                ],
              };
            },
            listInstanceNodes: async () => ({
              nodes: [
                create(InstanceNodeSchema, {
                  name: 'host-1',
                  agentUuid: 'a1',
                  node: { clientVersion: { major: 1, minor: 8, patch: 2 } },
                }),
                create(InstanceNodeSchema, { name: 'stray' }),
              ],
            }),
          },
        },
      },
    ],
  });
  if (answers.url) {
    await TestBed.inject(Router).navigateByUrl(answers.url);
  }
  const fixture = TestBed.createComponent(Host);
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
  return fixture;
}

describe('AgentsTable', () => {
  it('shows each agent with its state, cause and the version of its paired node', async () => {
    const fixture = await render({ agents: [agent('a1', 'lb-1')] });
    const row: HTMLElement = fixture.nativeElement.querySelector('tr.agent');

    expect(row.querySelector('.state-dot')?.classList).toContain('warning');
    expect(row.textContent).toContain('envoy: 2 clusters warming');
    expect(row.textContent).toContain('1.8.2');
  });

  it('lists a connected node no agent is paired with', async () => {
    const fixture = await render({ agents: [agent('a1', 'lb-1')] });

    expect(fixture.nativeElement.querySelector('.undeclared').textContent).toContain('stray');
  });

  it('says the instance has no agent instead of failing', async () => {
    const fixture = await render({ agents: [], statusFails: true });

    expect(fixture.nativeElement.textContent).toContain('This instance has no agent yet');
  });

  it('opens the agent panel on a row click', async () => {
    const fixture = await render({ agents: [agent('a1', 'lb-1')] });

    fixture.nativeElement.querySelector('tr.agent').click();
    await fixture.whenStable();

    expect(fixture.nativeElement.querySelector('flock-agent-panel')).not.toBeNull();
  });

  it('lists the provisioners in a field of their own, out of the health count', async () => {
    const fixture = await render({
      agents: [agent('a1', 'lb-1'), agent('p1', 'acme', Agent_Mode.PROVISIONER)],
    });
    const element: HTMLElement = fixture.nativeElement;

    expect(element.querySelectorAll('tr.agent').length).toBe(1);
    expect(element.querySelector('.provisioners')?.textContent).toContain('Provisioners');
    expect(element.querySelector('.provisioners')?.textContent).toContain('acme');
    expect(element.querySelector('.head .meta')?.textContent).toContain('0 / 1 healthy');
  });

  it('hides mode and announced VIPs in the panel of a provisioner', async () => {
    const fixture = await render({ agents: [agent('p1', 'acme', Agent_Mode.PROVISIONER)] });

    fixture.nativeElement.querySelector('.provisioners .provisioner').click();
    await fixture.whenStable();
    // the side panel moves itself to the body
    const panel = document.body.querySelector('flock-side-panel')!;

    expect(panel.textContent).toContain('Provisioner');
    expect(panel.textContent).toContain('acme');
    expect(panel.textContent).not.toContain('Mode');
    expect(panel.textContent).not.toContain('VIPs announced');
  });

  it('opens the panel of the agent the url names', async () => {
    const fixture = await render({
      agents: [agent('a1', 'lb-1'), agent('p1', 'acme', Agent_Mode.PROVISIONER)],
      url: '/?uuid=inst-1&agent=p1',
    });

    expect(fixture.nativeElement.querySelector('flock-agent-panel')).not.toBeNull();
    expect(document.body.querySelector('flock-side-panel')?.textContent).toContain('acme');
  });
});
