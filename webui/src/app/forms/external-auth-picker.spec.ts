import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { provideRouter } from '@angular/router';
import { create, DescMessage, Message } from '@bufbuild/protobuf';
import { of } from 'rxjs';
import {
  Route,
  RouteSchema,
  VirtualHost,
  VirtualHostSchema,
} from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { Api } from '../core/api';
import { Session } from '../core/session';
import { Access } from '../edit/access';
import { policyNamesOf } from './external-auth-picker';
import { ProtoForm } from './proto-form';
import { FieldError } from './rules';

@Component({
  imports: [ProtoForm],
  template: `<flock-proto-form
    [schema]="schema()"
    [(value)]="value"
    [policyNames]="names()"
    [errors]="errors()"
  />`,
})
class Host {
  schema = signal<DescMessage>(VirtualHostSchema);
  value = signal<Message>(create(VirtualHostSchema));
  names = signal<string[] | null>(['sso', 'other']);
  errors = signal<FieldError[]>([]);
}

async function render(schema: DescMessage, value: Message) {
  const fixture = TestBed.createComponent(Host);
  fixture.componentInstance.schema.set(schema);
  fixture.componentInstance.value.set(value);
  await fixture.whenStable();
  return fixture;
}

async function settle(fixture: { whenStable(): Promise<unknown> }) {
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
}

// the options of the select opened last, the panel of a select closed before may still be in the document
function openOptions(): HTMLElement[] {
  const panels = document.querySelectorAll('.mat-mdc-select-panel');
  return [...(panels[panels.length - 1]?.querySelectorAll<HTMLElement>('mat-option') ?? [])];
}

function optionTexts(): string[] {
  return openOptions().map((o) => (o.textContent ?? '').trim());
}

function option(text: string): HTMLElement {
  const found = openOptions().find((o) => o.textContent?.trim() === text);
  if (!found) {
    throw new Error(`no option ${text}`);
  }
  return found;
}

async function openPicker(fixture: { nativeElement: HTMLElement; whenStable(): Promise<unknown> }) {
  const picker = fixture.nativeElement.querySelector('flock-external-auth-picker')!;
  picker.querySelector<HTMLElement>('.mat-mdc-select-trigger')!.click();
  await settle(fixture);
}

describe('ExternalAuthPicker', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: Api, useValue: { list: vi.fn(async () => []) } },
        {
          provide: Access,
          useValue: { check: async () => null, features: async () => ({ ipamEnabled: false }) },
        },
        {
          provide: Session,
          useValue: { user: () => ({ username: 'alice', groups: [], isAdmin: false }) },
        },
        { provide: MatDialog, useValue: { open: () => ({ afterClosed: () => of(true) }) } },
      ],
    });
  });

  afterEach(() => {
    document
      .querySelectorAll('flock-side-panel, .cdk-overlay-container')
      .forEach((e) => e.remove());
  });

  it('offers none and the policy names on a virtual host', async () => {
    const fixture = await render(VirtualHostSchema, create(VirtualHostSchema));

    await openPicker(fixture);
    expect(optionTexts()).toEqual(['None', 'sso', 'other']);

    option('sso').click();
    await settle(fixture);
    expect((fixture.componentInstance.value() as VirtualHost).externalAuth).toBe('sso');
  });

  it('offers inherit, disabled and the names on a route', async () => {
    const fixture = await render(RouteSchema, create(RouteSchema, { name: 'default' }));

    await openPicker(fixture);
    expect(optionTexts()).toEqual(['Inherit', 'Disabled', 'sso', 'other']);

    option('Disabled').click();
    await settle(fixture);
    expect((fixture.componentInstance.value() as Route).externalAuth).toEqual({
      case: 'externalAuthDisabled',
      value: true,
    });

    await openPicker(fixture);
    option('other').click();
    await settle(fixture);
    expect((fixture.componentInstance.value() as Route).externalAuth).toEqual({
      case: 'externalAuthName',
      value: 'other',
    });

    await openPicker(fixture);
    option('Inherit').click();
    await settle(fixture);
    expect((fixture.componentInstance.value() as Route).externalAuth.case).toBeUndefined();
  });

  it('keeps showing a stored name no policy defines', async () => {
    const fixture = await render(
      VirtualHostSchema,
      create(VirtualHostSchema, { externalAuth: 'gone' }),
    );

    await openPicker(fixture);
    expect(optionTexts()).toEqual(['None', 'sso', 'other', 'gone']);
  });

  it('shows the effective policy in a route row', async () => {
    const vhost = create(VirtualHostSchema, {
      externalAuth: 'sso',
      routes: [
        { name: 'default' },
        { name: '/open/', externalAuth: { case: 'externalAuthDisabled', value: true } },
        { name: 'admin', externalAuth: { case: 'externalAuthName', value: 'other' } },
      ],
    });
    const fixture = await render(VirtualHostSchema, vhost);

    const rows = [
      ...(fixture.nativeElement as HTMLElement).querySelectorAll('[data-field="routes"] .open'),
    ].map((row) => row.textContent?.trim());
    expect(rows).toEqual(['default · sso (inherited)', '/open/ · disabled', 'admin · other']);
  });

  it('keeps the route row a plain name when nothing checks it', async () => {
    const vhost = create(VirtualHostSchema, { routes: [{ name: 'default' }] });
    const fixture = await render(VirtualHostSchema, vhost);

    const row = (fixture.nativeElement as HTMLElement).querySelector('[data-field="routes"] .open');
    expect(row?.textContent?.trim()).toBe('default');
  });

  it('shows the error of a route under its picker in the stacked panel of the route', async () => {
    const vhost = create(VirtualHostSchema, {
      routes: [{ name: 'default', externalAuth: { case: 'externalAuthName', value: 'nope' } }],
    });
    const fixture = await render(VirtualHostSchema, vhost);
    fixture.componentInstance.errors.set([
      {
        path: 'routes[0].external_auth',
        message: 'external auth nope is not defined in external_auths',
      },
    ]);
    await settle(fixture);

    fixture.nativeElement.querySelector('[data-field="routes"] .open')!.click();
    await settle(fixture);

    const panel = document.body.querySelector('flock-side-panel')!;
    const picker = panel.querySelector('flock-external-auth-picker')!;
    const error = picker.nextElementSibling;
    expect(error?.classList).toContain('field-error');
    expect(error?.textContent).toBe('external auth nope is not defined in external_auths');
  });

  it('reads the policy names of a virtual server, and none of another kind', () => {
    const vs = create(VirtualServerSchema, {
      routeConfig: {
        case: 'httpRoutesConfig',
        value: { externalAuths: [{ name: 'sso' }, { name: '' }, { name: 'other' }] },
      },
    });

    expect(policyNamesOf(VirtualServerSchema, vs)).toEqual(['sso', 'other']);
    expect(policyNamesOf(VirtualServerSchema, create(VirtualServerSchema))).toEqual([]);
    expect(policyNamesOf(BackendSchema, create(BackendSchema))).toBeNull();
  });
});
