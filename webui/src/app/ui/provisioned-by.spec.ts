import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { Agent_Mode, AgentSchema } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { CertificateSchema } from '../../gen/flock/api/config/cert/v1/cert_pb';
import { Api } from '../core/api';
import { ProvisionedBy, provisionerUuidOf } from './provisioned-by';

@Component({
  imports: [ProvisionedBy],
  template: `<flock-provisioned-by agentUuid="p1" />`,
})
class Host {}

async function render(readAgent: () => Promise<unknown>): Promise<ComponentFixture<Host>> {
  TestBed.configureTestingModule({
    providers: [provideRouter([]), { provide: Api, useValue: { lookup: { readAgent } } }],
  });
  const fixture = TestBed.createComponent(Host);
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
  return fixture;
}

describe('ProvisionedBy', () => {
  it('names the provisioner and links to its panel in the instances page', async () => {
    const agent = create(AgentSchema, { UUID: 'p1', name: 'acme', mode: Agent_Mode.PROVISIONER });
    const fixture = await render(async () => ({ agent, instanceUuid: 'inst-1' }));

    const link: HTMLAnchorElement = fixture.nativeElement.querySelector('a');
    expect(fixture.nativeElement.textContent).toContain('Provisioned by');
    expect(link.textContent).toContain('acme');
    expect(link.getAttribute('href')).toBe('/instances?uuid=inst-1&agent=p1');
  });

  it('shows the raw uuid with no link when no declared agent has it', async () => {
    const fixture = await render(async () => {
      throw new ConnectError('agent p1 not found', Code.NotFound);
    });

    expect(fixture.nativeElement.querySelector('a')).toBeNull();
    expect(fixture.nativeElement.textContent).toContain('p1');
  });

  it('reads the provisioner uuid from the metadata of a resource', () => {
    const provisioned = create(CertificateSchema, { metadata: { 'flock.provision.by': 'p1' } });

    expect(provisionerUuidOf(provisioned)).toBe('p1');
    expect(provisionerUuidOf(create(CertificateSchema))).toBe('');
  });
});
