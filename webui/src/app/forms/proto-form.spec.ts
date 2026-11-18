import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { provideRouter } from '@angular/router';
import { create, DescMessage, Message } from '@bufbuild/protobuf';
import { anyPack, AnySchema } from '@bufbuild/protobuf/wkt';
import { of } from 'rxjs';
import { Backend, BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { Certificate, CertificateSchema } from '../../gen/flock/api/config/cert/v1/cert_pb';
import { SecretSchema } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { HealthCheckSchema } from '../../gen/flock/api/config/core/v1/healthcheck_pb';
import {
  HttpRoutesConfigSchema,
  TcpRouteConfig,
  TcpRouteConfigSchema,
  TlsTcpRoutesConfig,
  TlsTcpRoutesConfigSchema,
  VirtualServer,
  VirtualServerSchema,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import {
  RouteMatchSchema,
  VirtualHost,
  VirtualHostSchema,
} from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { VIP, VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import {
  ExternalAuth,
  ExternalAuthSchema,
} from '../../gen/flock/api/config/route/v1/external_auth_pb';
import { StarlingAnnounceSchema } from '../../gen/flock/extensions/announces/starling_announce/v1/announce_pb';
import { Api } from '../core/api';
import { nameOrUuidFilter } from '../core/resources';
import { ResourceCreations } from '../core/resource-creations';
import { Session } from '../core/session';
import { Access } from '../edit/access';
import { AgentKinds, ProtoForm } from './proto-form';
import { FieldError } from './rules';

@Component({
  imports: [ProtoForm],
  template: `
    <flock-proto-form
      [schema]="schema()"
      [(value)]="value"
      [agentKinds]="agentKinds()"
      [readonly]="readonly()"
      [errors]="errors()"
      (dirty)="dirty = $event"
    />
  `,
})
class Host {
  schema = signal<DescMessage>(BackendSchema);
  value = signal<Message>(create(BackendSchema));
  agentKinds = signal<AgentKinds | null>(null);
  readonly = signal<string[]>([]);
  errors = signal<FieldError[]>([]);
  dirty = false;
}

const backendUuid = '11111111-2222-3333-4444-555555555555';
const backends = [create(BackendSchema, { UUID: backendUuid, name: 'web-backend' })];
let api: { list: ReturnType<typeof vi.fn> };

async function render(
  schema: DescMessage,
  value: Message,
  agentKinds: AgentKinds | null = null,
  readonly: string[] = [],
) {
  const fixture = TestBed.createComponent(Host);
  fixture.componentInstance.schema.set(schema);
  fixture.componentInstance.value.set(value);
  fixture.componentInstance.agentKinds.set(agentKinds);
  fixture.componentInstance.readonly.set(readonly);
  await fixture.whenStable();
  return fixture;
}

function type(input: HTMLInputElement, text: string) {
  input.value = text;
  input.dispatchEvent(new Event('input'));
}

function input(root: Element, name: string): HTMLInputElement {
  const found = root.querySelector<HTMLInputElement>(`input[name="${name}"]`);
  if (!found) {
    throw new Error(`no input ${name}`);
  }
  return found;
}

function button(root: Element, text: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === text,
  );
  if (!found) {
    throw new Error(`no button ${text}`);
  }
  return found;
}

describe('ProtoForm', () => {
  beforeEach(() => {
    api = { list: vi.fn(async () => backends) };
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: Api, useValue: api },
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

  it('asks the health checker of a health check first', async () => {
    const fixture = await render(HealthCheckSchema, create(HealthCheckSchema));

    const rows = [...(fixture.nativeElement as HTMLElement).querySelectorAll(':scope .row')];
    const names = rows.map((row) => row.getAttribute('data-field'));
    expect(names[0]).toBe('health_checker');
    expect(names.filter((name) => name === 'health_checker').length).toBe(1);
  });

  it('edits a Backend name and a nested health check field', async () => {
    const stored = create(BackendSchema, {
      name: 'web',
      healthCheck: create(HealthCheckSchema, { unhealthyThreshold: 1 }),
    });
    const fixture = await render(BackendSchema, stored);
    const root: HTMLElement = fixture.nativeElement;

    type(input(root, 'name'), 'api');
    await fixture.whenStable();
    const healthCheck = root.querySelector('[data-field="health_check"]')!;
    healthCheck.querySelector<HTMLButtonElement>('.advanced-toggle')!.click();
    await fixture.whenStable();
    type(input(healthCheck, 'unhealthy_threshold'), '3');
    await fixture.whenStable();

    const edited = fixture.componentInstance.value() as Backend;
    expect(edited.name).toBe('api');
    expect(edited.healthCheck?.unhealthyThreshold).toBe(3);
    expect(fixture.componentInstance.dirty).toBe(true);
    // the input message is never edited in place
    expect(stored.name).toBe('web');
    expect(stored.healthCheck?.unhealthyThreshold).toBe(1);
  });

  it('shows the fields of a picked health checker right under its cards', async () => {
    const fixture = await render(BackendSchema, create(BackendSchema, { name: 'web' }));
    const healthCheck = (fixture.nativeElement as HTMLElement).querySelector(
      '[data-field="health_check"]',
    )!;

    healthCheck.querySelector<HTMLElement>('[data-choice="httpHealthCheck"]')!.click();
    await fixture.whenStable();
    type(input(healthCheck, 'path'), '/healthz');
    await fixture.whenStable();

    const checker = (fixture.componentInstance.value() as Backend).healthCheck!.healthChecker;
    expect(checker.case === 'httpHealthCheck' && checker.value.path).toBe('/healthz');
    expect(document.body.querySelector('flock-side-panel')).toBeNull();
  });

  it('shows nothing under the card of a case with no field', async () => {
    const fixture = await render(BackendSchema, create(BackendSchema, { name: 'web' }));
    const healthCheck = (fixture.nativeElement as HTMLElement).querySelector(
      '[data-field="health_check"]',
    )!;

    healthCheck.querySelector<HTMLElement>('[data-choice="noHealthCheck"]')!.click();
    await fixture.whenStable();

    expect(healthCheck.querySelector('.case')!.children.length).toBe(0);
  });

  it('folds the rarely changed fields of a health check under Advanced settings', async () => {
    const fixture = await render(HealthCheckSchema, create(HealthCheckSchema));
    const root: HTMLElement = fixture.nativeElement;
    expect(root.querySelector('input[name="interval"]')).not.toBeNull();
    expect(root.querySelector('input[name="unhealthy_threshold"]')).toBeNull();

    root.querySelector<HTMLButtonElement>('.advanced-toggle')!.click();
    await fixture.whenStable();

    expect(root.querySelector('input[name="unhealthy_threshold"]')).not.toBeNull();
  });

  it('keeps Advanced settings open while one of its fields has an error', async () => {
    const fixture = await render(HealthCheckSchema, create(HealthCheckSchema));
    const root: HTMLElement = fixture.nativeElement;

    fixture.componentInstance.errors.set([{ path: 'alt_port', message: 'too big' }]);
    await fixture.whenStable();

    expect(root.querySelector('input[name="alt_port"]')).not.toBeNull();
    expect(root.querySelector<HTMLButtonElement>('.advanced-toggle')!.disabled).toBe(true);
  });

  it('shows the default of an empty field and no 0 for an unset number', async () => {
    const fixture = await render(HealthCheckSchema, create(HealthCheckSchema));
    const root: HTMLElement = fixture.nativeElement;

    expect(input(root, 'timeout').placeholder).toBe('30s by default');
    root.querySelector<HTMLButtonElement>('.advanced-toggle')!.click();
    await fixture.whenStable();
    expect(input(root, 'unhealthy_threshold').value).toBe('');
    expect(input(root, 'unhealthy_threshold').placeholder).toBe('3 by default');
    expect(input(root, 'alt_port').value).toBe('');
  });

  it('edits the PEM blocks of a certificate in text areas', async () => {
    const fixture = await render(CertificateSchema, create(CertificateSchema));
    const root: HTMLElement = fixture.nativeElement;
    const pem = '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----';

    const areas = [...root.querySelectorAll<HTMLTextAreaElement>('textarea')].map(
      (area) => area.name,
    );
    expect(areas).toEqual(['cert_chain', 'private_key', 'ca']);
    const certChain = root.querySelector<HTMLTextAreaElement>('textarea[name="cert_chain"]')!;
    certChain.value = pem;
    certChain.dispatchEvent(new Event('input'));
    await fixture.whenStable();

    expect((fixture.componentInstance.value() as Certificate).certChain).toBe(pem);
  });

  it('writes the acronyms of a card title and drops a line that only repeats it', async () => {
    const fixture = await render(HealthCheckSchema, create(HealthCheckSchema));
    const card = (fixture.nativeElement as HTMLElement).querySelector(
      '[data-choice="grpcHealthCheck"]',
    )!;

    expect(card.querySelector('.card-title')!.textContent).toBe('gRPC health check');
    expect(card.querySelector('.card-line')).toBeNull();
  });

  it('adds a route to a VirtualServer through the stacked panel', async () => {
    const vs = create(VirtualServerSchema, {
      name: 'web',
      port: 443,
      routeConfig: { case: 'tlsTcpRoutesConfig', value: create(TlsTcpRoutesConfigSchema) },
    });
    const fixture = await render(VirtualServerSchema, vs);
    const root: HTMLElement = fixture.nativeElement;

    button(root.querySelector('[data-field="routes"]')!, 'Add').click();
    await fixture.whenStable();
    const panel = document.body.querySelector('flock-side-panel')!;
    expect(panel).not.toBeNull();
    type(input(panel, 'backend_uuid'), backendUuid);
    await fixture.whenStable();
    button(panel, 'Apply to virtual server').click();
    await fixture.whenStable();

    const edited = fixture.componentInstance.value() as VirtualServer;
    const routes = (edited.routeConfig.value as TlsTcpRoutesConfig).routes;
    expect(routes.map((r) => r.backendUuid)).toEqual([backendUuid]);
    expect(document.body.querySelector('flock-side-panel')).toBeNull();
  });

  it('keeps unknown typed_options on save', async () => {
    const unknown = create(AnySchema, {
      typeUrl: 'type.googleapis.com/acme.extensions.v1.Options',
      value: new Uint8Array([8, 1, 18, 2, 104, 105]),
    });
    const stored = create(BackendSchema, { name: 'web', typedOptions: unknown });
    const fixture = await render(BackendSchema, stored);
    const root: HTMLElement = fixture.nativeElement;

    expect(root.textContent).toContain('Not supported by the agents of this resource');
    type(input(root, 'name'), 'api');
    await fixture.whenStable();

    const edited = fixture.componentInstance.value() as Backend;
    expect(edited.name).toBe('api');
    expect(edited.typedOptions?.typeUrl).toBe(unknown.typeUrl);
    expect(edited.typedOptions?.value).toEqual(unknown.value);
  });

  it('lists backends in the uuid picker of backend_uuid and writes the picked UUID', async () => {
    const fixture = await render(TcpRouteConfigSchema, create(TcpRouteConfigSchema));
    const picker = input(fixture.nativeElement, 'backend_uuid');

    picker.dispatchEvent(new Event('focusin'));
    type(picker, 'web');
    await fixture.whenStable();
    await new Promise((resolve) => setTimeout(resolve));
    await fixture.whenStable();

    expect(api.list).toHaveBeenCalledWith('backend', {
      limit: 20,
      offset: 0,
      filters: [nameOrUuidFilter('web')],
    });
    const option = [...document.querySelectorAll<HTMLElement>('mat-option')].find((o) =>
      o.textContent?.includes('web-backend'),
    );
    expect(option?.textContent).toContain(backendUuid);
    option!.click();
    await fixture.whenStable();

    expect((fixture.componentInstance.value() as TcpRouteConfig).backendUuid).toBe(backendUuid);
  });

  it('asks for a backend creation from the first entry of the picker and picks it once created', async () => {
    const fixture = await render(TcpRouteConfigSchema, create(TcpRouteConfigSchema));
    const picker = input(fixture.nativeElement, 'backend_uuid');
    const creations = TestBed.inject(ResourceCreations);

    picker.dispatchEvent(new Event('focusin'));
    await fixture.whenStable();
    await new Promise((resolve) => setTimeout(resolve));
    await fixture.whenStable();
    const createEntry = document.querySelector<HTMLElement>('mat-option')!;
    expect(createEntry.textContent).toContain('Create a backend');
    createEntry.click();
    await fixture.whenStable();

    const [creation] = creations.open();
    expect(creation.kind).toBe('backend');
    creation.created(create(BackendSchema, { UUID: 'backend-new', name: 'api' }));
    await fixture.whenStable();
    expect((fixture.componentInstance.value() as TcpRouteConfig).backendUuid).toBe('backend-new');
  });

  it('shows the agent kinds outside agentKinds disabled with the reason', async () => {
    const fixture = await render(HttpRoutesConfigSchema, create(HttpRoutesConfigSchema), {
      kinds: [],
      reason: 'r',
    });
    const anyField = fixture.nativeElement.querySelector('[data-field="typed_options"]');

    anyField.querySelector('.mat-mdc-select-trigger').click();
    await fixture.whenStable();

    const starling = [...document.querySelectorAll<HTMLElement>('mat-option')].find((o) =>
      o.textContent?.includes('starling'),
    );
    expect(starling).toBeDefined();
    expect(starling!.getAttribute('aria-disabled')).toBe('true');
    expect(starling!.querySelector('.reason')?.textContent?.trim()).toBe('r');
  });

  it('disables the agent kinds outside a non-empty shared list', async () => {
    const fixture = await render(HttpRoutesConfigSchema, create(HttpRoutesConfigSchema), {
      kinds: ['flock.extensions.other.v1.Shared'],
      reason: '',
    });
    const anyField = fixture.nativeElement.querySelector('[data-field="typed_options"]');

    anyField.querySelector('.mat-mdc-select-trigger').click();
    await fixture.whenStable();

    const starling = [...document.querySelectorAll<HTMLElement>('mat-option')].find((o) =>
      o.textContent?.includes('starling'),
    );
    expect(starling!.getAttribute('aria-disabled')).toBe('true');
    expect(starling!.querySelector('.reason')?.textContent?.trim()).toBe(
      'Not every agent of this resource supports it.',
    );
  });

  it('shows a read-only Any with its oneof and the case form read-only', async () => {
    const announce = create(StarlingAnnounceSchema, {
      networkConfig: { case: 'bgp', value: { as: 65000n } },
    });
    const vip = create(VIPSchema, {
      name: 'web',
      typedAnnounce: anyPack(StarlingAnnounceSchema, announce),
    });
    const fixture = await render(VIPSchema, vip, null, ['typed_announce']);
    const anyField: HTMLElement = fixture.nativeElement.querySelector(
      '[data-field="typed_announce"]',
    );

    const cards = anyField.querySelectorAll<HTMLButtonElement>(
      '[data-oneof="network_config"] button',
    );
    expect([...cards].every((card) => card.disabled)).toBe(true);
    const bgp = anyField.querySelector('[data-field="bgp"]')!;
    expect(input(bgp, 'as').disabled).toBe(true);
  });

  it('shows a required oneof of messages as cards with the start of their docs, and no None', async () => {
    const fixture = await render(BackendSchema, create(BackendSchema, { name: 'web' }));
    const root: HTMLElement = fixture.nativeElement;

    const cards = [...root.querySelectorAll<HTMLElement>('[data-oneof="endpoint"] button')];
    expect(cards.map((card) => card.getAttribute('data-choice'))).toEqual([
      'dns',
      'ipAddrGroup',
      'eds',
    ]);
    expect(cards[0].querySelector('.card-line')!.textContent).toBe(
      'Endpoints resolved from a DNS name',
    );

    cards[0].click();
    await fixture.whenStable();
    expect((fixture.componentInstance.value() as Backend).endpoint.case).toBe('dns');
    expect(root.querySelector('[data-field="dns"]')).not.toBeNull();
  });

  it('keeps what a case holds when its card is picked again', async () => {
    const stored = create(BackendSchema, {
      name: 'web',
      endpoint: { case: 'dns', value: { host: 'web.local', port: 80 } },
    });
    const fixture = await render(BackendSchema, stored);

    (fixture.nativeElement as HTMLElement)
      .querySelector<HTMLElement>('[data-choice="dns"]')!
      .click();
    await fixture.whenStable();

    const endpoint = (fixture.componentInstance.value() as Backend).endpoint;
    expect(endpoint.case === 'dns' && endpoint.value.host).toBe('web.local');
  });

  it('sets the domains of a virtual host to * in one click', async () => {
    const fixture = await render(
      VirtualHostSchema,
      create(VirtualHostSchema, { domains: ['api.example.com', 'www.example.com'] }),
    );
    const shortcut = button(fixture.nativeElement, 'Any domain (*)');

    shortcut.click();
    await fixture.whenStable();

    expect((fixture.componentInstance.value() as VirtualHost).domains).toEqual(['*']);
    expect(shortcut.disabled).toBe(true);
    const chip = (fixture.nativeElement as HTMLElement).querySelector(
      '[data-field="domains"] mat-chip-row',
    )!;
    expect(chip.textContent).toContain('any domain');
    expect(chip.textContent).not.toContain('*');
  });

  it('lowercases the header names of an external auth policy as they are added', async () => {
    const fixture = await render(ExternalAuthSchema, create(ExternalAuthSchema, { name: 'sso' }));
    const chipInput = (fixture.nativeElement as HTMLElement).querySelector<HTMLInputElement>(
      '[data-field="upstream_headers"] input',
    )!;

    chipInput.value = 'X-User';
    // MatChipInput reads keyCode, which the KeyboardEvent constructor leaves at 0
    const enter = new KeyboardEvent('keydown', { key: 'Enter' });
    Object.defineProperty(enter, 'keyCode', { value: 13 });
    chipInput.dispatchEvent(enter);
    await fixture.whenStable();

    expect((fixture.componentInstance.value() as ExternalAuth).upstreamHeaders).toEqual(['x-user']);
  });

  it('keeps a select for a oneof of plain values', async () => {
    const fixture = await render(RouteMatchSchema, create(RouteMatchSchema));
    const root: HTMLElement = fixture.nativeElement;

    expect(root.querySelector('mat-select[name="path_specifier"]')).not.toBeNull();
    expect(root.querySelector('flock-choice-cards')).toBeNull();
  });

  it('makes a oneof read-only when readonly names one of its fields', async () => {
    const vs = create(VirtualServerSchema, {
      name: 'web',
      routeConfig: { case: 'tcpRouteConfig', value: { backendUuid: backendUuid } },
    });
    const fixture = await render(VirtualServerSchema, vs, null, ['tcp_route_config']);
    const root: HTMLElement = fixture.nativeElement;

    const cards = root.querySelectorAll<HTMLButtonElement>('[data-oneof="route_config"] button');
    expect([...cards].every((card) => card.disabled)).toBe(true);
    const route = root.querySelector('[data-field="tcp_route_config"]')!;
    expect(input(route, 'backend_uuid').disabled).toBe(true);
    expect(input(root, 'name').disabled).toBe(false);
  });

  it('keeps a map row with no key yet while another field changes', async () => {
    const stored = create(BackendSchema, { name: 'web', metadata: { team: 'lb' } });
    const fixture = await render(BackendSchema, stored);
    const root: HTMLElement = fixture.nativeElement;
    const metadata = () => root.querySelector('[data-field="metadata"]')!;

    button(metadata(), 'Add').click();
    await fixture.whenStable();
    expect(metadata().querySelectorAll('.entry').length).toBe(2);
    type(input(root, 'name'), 'api');
    await fixture.whenStable();

    expect(metadata().querySelectorAll('.entry').length).toBe(2);
    expect((fixture.componentInstance.value() as Backend).metadata).toEqual({ team: 'lb' });
  });

  it('keeps an Any whose bytes do not decode as it is', async () => {
    const broken = create(AnySchema, {
      typeUrl:
        'type.googleapis.com/flock.extensions.announces.starling_announce.v1.StarlingAnnounce',
      value: new Uint8Array([255, 255, 255]),
    });
    const fixture = await render(
      VIPSchema,
      create(VIPSchema, { name: 'web', typedAnnounce: broken }),
    );
    const root: HTMLElement = fixture.nativeElement;

    expect(root.querySelector('[data-field="typed_announce"]')!.textContent).toContain(
      "can't be edited here",
    );
    type(input(root, 'name'), 'api');
    await fixture.whenStable();

    const edited = fixture.componentInstance.value() as VIP;
    expect(edited.name).toBe('api');
    expect(edited.typedAnnounce?.typeUrl).toBe(broken.typeUrl);
    expect(edited.typedAnnounce?.value).toEqual(broken.value);
  });

  it('shows the values of a stored secret as empty password inputs that read unchanged', async () => {
    const stored = create(SecretSchema, { name: 'dns', data: { API_TOKEN: '' } });
    const fixture = await render(SecretSchema, stored);
    const root: HTMLElement = fixture.nativeElement;
    const rows = () => root.querySelectorAll('[data-field="data"] .entry');
    const values = () =>
      Array.from(rows()).map((row) => row.querySelectorAll<HTMLInputElement>('input')[1]);

    expect(values().map((v) => [v.type, v.value, v.placeholder])).toEqual([
      ['password', '', 'unchanged'],
    ]);
    button(root.querySelector('[data-field="data"]')!, 'Add').click();
    await fixture.whenStable();

    expect(values().map((v) => v.placeholder)).toEqual(['unchanged', '']);
    expect(values().every((v) => v.type === 'password')).toBe(true);
  });
});
