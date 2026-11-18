import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { clone, create } from '@bufbuild/protobuf';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { ExternalAuthSchema } from '../../gen/flock/api/config/route/v1/external_auth_pb';
import {
  HttpRoutesConfig,
  VirtualServerSchema,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { AgentKinds, ProtoForm } from '../forms/proto-form';
import { EditCard } from './card';
import { CardDef } from './cards';

@Component({
  imports: [EditCard],
  template: `
    <flock-edit-card
      [def]="def()"
      [schema]="schema"
      [value]="value"
      [creating]="creating()"
      [loadAgentKinds]="load"
    />
  `,
})
class Host {
  def = signal<CardDef>({ title: 'Advanced', fields: ['typed_options'], advanced: true });
  schema = BackendSchema;
  value = create(BackendSchema, { UUID: 'b1', name: 'web' });
  creating = signal(false);
  load = vi.fn(async (): Promise<AgentKinds> => ({ kinds: 'any', reason: 'No instance yet' }));
}

async function setup(def?: CardDef) {
  const fixture = TestBed.createComponent(Host);
  if (def) {
    fixture.componentInstance.def.set(def);
  }
  fixture.detectChanges();
  await fixture.whenStable();
  return fixture;
}

async function click(fixture: Awaited<ReturnType<typeof setup>>, selector: string) {
  fixture.nativeElement.querySelector(selector).click();
  await fixture.whenStable();
  fixture.detectChanges();
}

describe('EditCard agent kinds', () => {
  it('loads them on each edit session of an advanced card, never at render', async () => {
    const fixture = await setup();
    const load = fixture.componentInstance.load;
    expect(load).not.toHaveBeenCalled();

    await click(fixture, 'button.edit');
    expect(load).toHaveBeenCalledTimes(1);
    expect(fixture.nativeElement.textContent).toContain('No instance yet');

    await click(fixture, 'button.cancel');
    await click(fixture, 'button.edit');
    expect(load).toHaveBeenCalledTimes(2);
  });

  it('never loads them for a card that is not advanced', async () => {
    const fixture = await setup({ title: 'Main', fields: ['name'] });

    await click(fixture, 'button.edit');

    expect(fixture.componentInstance.load).not.toHaveBeenCalled();
  });

  it('offers any kind with a reason when the load fails', async () => {
    const fixture = await setup();
    fixture.componentInstance.load.mockRejectedValue(new Error('boom'));

    await click(fixture, 'button.edit');

    expect(fixture.nativeElement.textContent).toContain('The agents could not be read');
  });
});

describe('EditCard fields', () => {
  const general: CardDef = { title: 'General', fields: ['UUID', 'name'] };

  it('leaves the UUID out of a create form, the controller gives it', async () => {
    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.def.set(general);
    fixture.componentInstance.creating.set(true);
    await fixture.whenStable();

    const root: HTMLElement = fixture.nativeElement;
    expect(root.querySelector('input[name="UUID"]')).toBeNull();
    expect(root.querySelector('input[name="name"]')).not.toBeNull();
  });

  it('shows the UUID of a stored resource', async () => {
    const fixture = await setup(general);

    expect(fixture.nativeElement.querySelector('input[name="UUID"]')).not.toBeNull();
  });
});

describe('EditCard policy names', () => {
  it('hands the forms the policies of the virtual server as edited, saved or not', async () => {
    const fixture = TestBed.createComponent(EditCard);
    const stored = create(VirtualServerSchema, {
      UUID: 'vs1',
      name: 'web',
      routeConfig: { case: 'httpRoutesConfig', value: { externalAuths: [{ name: 'sso' }] } },
    });
    fixture.componentRef.setInput('def', { title: 'Routes', fields: ['route_config'] });
    fixture.componentRef.setInput('schema', VirtualServerSchema);
    fixture.componentRef.setInput('value', stored);
    await fixture.whenStable();
    const form = () => fixture.debugElement.query(By.directive(ProtoForm)).componentInstance;
    expect(form().policyNames()).toEqual(['sso']);

    fixture.nativeElement.querySelector('button.edit').click();
    await fixture.whenStable();
    const edited = clone(VirtualServerSchema, stored);
    (edited.routeConfig.value as HttpRoutesConfig).externalAuths.push(
      create(ExternalAuthSchema, { name: 'other' }),
    );
    form().value.set(edited);
    await fixture.whenStable();

    expect(form().policyNames()).toEqual(['sso', 'other']);
  });
});
