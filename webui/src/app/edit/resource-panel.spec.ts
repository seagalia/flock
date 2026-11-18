import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { MatTooltip } from '@angular/material/tooltip';
import { By } from '@angular/platform-browser';
import { provideRouter, Router } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { AnySchema } from '@bufbuild/protobuf/wkt';
import { of } from 'rxjs';
import { Backend, BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { CertificateSchema } from '../../gen/flock/api/config/cert/v1/cert_pb';
import { ZoneSchema } from '../../gen/flock/api/config/zone/v1/zone_pb';
import { Api } from '../core/api';
import { KindName, Resource } from '../core/resources';
import { Session } from '../core/session';
import { Access } from './access';
import { ChangeData } from './confirm-change';
import { lockReason } from './lock';
import { editMenuItems, KindRules, lockKeyRefusal, ResourcePanel } from './resource-panel';

@Component({
  imports: [ResourcePanel],
  template: `
    <flock-resource-panel
      [kind]="kind()"
      [resource]="resource()"
      [prefill]="prefill()"
      [rules]="rules()"
      (changed)="changes = changes + 1"
      (closed)="closes = closes + 1"
    />
  `,
})
class Host {
  kind = signal<KindName>('backend');
  resource = signal<Resource | null>(null);
  prefill = signal<Resource | null>(null);
  rules = signal<KindRules>({});
  changes = 0;
  closes = 0;
}

const unknownAny = create(AnySchema, {
  typeUrl: 'type.googleapis.com/acme.extensions.v1.Options',
  value: new Uint8Array([8, 1]),
});

function storedBackend(): Backend {
  return create(BackendSchema, {
    UUID: 'b1',
    name: 'web',
    metadata: { team: 'lb' },
    typedOptions: unknownAny,
    endpoint: { case: 'dns', value: { host: 'web.local', port: 80 } },
    healthCheck: {
      timeout: { seconds: 1n },
      interval: { seconds: 5n },
      unhealthyThreshold: 3,
      healthyThreshold: 2,
      healthChecker: { case: 'noHealthCheck', value: {} },
    },
    refreshRate: { seconds: 5n },
  });
}

let backendApi: Record<string, ReturnType<typeof vi.fn>>;
let list: ReturnType<typeof vi.fn>;
let check: ReturnType<typeof vi.fn>;
let dialogs: unknown[];
let answer: boolean;

function setup() {
  backendApi = {
    readBackend: vi.fn(async () => ({ backend: storedBackend() })),
    updateBackend: vi.fn(async (req: { backend: Backend }) => ({ backend: req.backend })),
    createBackend: vi.fn(async (req: { backend: Backend }) => ({ backend: req.backend })),
    deleteBackend: vi.fn(async () => ({})),
  };
  list = vi.fn(async () => []);
  check = vi.fn(async () => null);
  dialogs = [];
  answer = true;
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      {
        provide: Api,
        useValue: {
          backend: backendApi,
          list,
          permission: {
            listMemberPermissions: vi.fn(async () => ({
              memberPermissions: [],
              allowAllRead: false,
            })),
          },
        },
      },
      {
        provide: Access,
        useValue: { check, features: async () => ({ ipamEnabled: false }) },
      },
      {
        provide: Session,
        useValue: { user: () => ({ username: 'alice', groups: [], isAdmin: false }) },
      },
      {
        provide: MatDialog,
        useValue: {
          open: (_: unknown, config: { data: unknown }) => {
            dialogs.push(config.data);
            return { afterClosed: () => of(answer) };
          },
        },
      },
    ],
  });
}

async function settle(fixture: { whenStable(): Promise<unknown> }) {
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
}

async function render(
  resource: Resource | null,
  kind: KindName = 'backend',
  prefill?: Resource,
  rules: KindRules = {},
) {
  const fixture = TestBed.createComponent(Host);
  fixture.componentInstance.rules.set(rules);
  fixture.componentInstance.kind.set(kind);
  fixture.componentInstance.resource.set(resource);
  fixture.componentInstance.prefill.set(prefill ?? null);
  await settle(fixture);
  return fixture;
}

function panel(): HTMLElement {
  return document.body.querySelector('flock-side-panel')!;
}

function card(title: string): HTMLElement {
  return panel().querySelector(`[data-card="${title}"]`)!;
}

function button(root: Element, text: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
    b.textContent?.trim().endsWith(text),
  );
  if (!found) {
    throw new Error(`no button ${text}`);
  }
  return found;
}

function type(input: HTMLInputElement, text: string) {
  input.value = text;
  input.dispatchEvent(new Event('input'));
}

function tooltipOf(fixture: ReturnType<typeof TestBed.createComponent>, element: Element) {
  const debug = fixture.debugElement.query(By.css('flock-resource-panel'));
  const found = debug.queryAll(By.directive(MatTooltip)).find((d) => d.nativeElement === element);
  return found?.injector.get(MatTooltip).message;
}

describe('ResourcePanel', () => {
  beforeEach(setup);

  afterEach(() => {
    document
      .querySelectorAll('flock-side-panel, .cdk-overlay-container')
      .forEach((e) => e.remove());
  });

  it("saves the stored resource plus the card's changes", async () => {
    const fixture = await render(storedBackend());

    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(card('General'), 'Save').click();
    await settle(fixture);

    expect(check).toHaveBeenCalledTimes(1);
    expect(backendApi['readBackend']).toHaveBeenCalledWith({ UUID: 'b1' });
    const confirmation = dialogs[0] as ChangeData;
    expect(confirmation.changes).toEqual([{ path: 'name', before: '"web"', after: '"api"' }]);
    const sent = backendApi['updateBackend'].mock.calls[0][0];
    expect(sent.UUID).toBe('b1');
    expect(sent.backend.name).toBe('api');
    expect(sent.backend.metadata).toEqual({ team: 'lb' });
    expect(sent.backend.typedOptions.typeUrl).toBe(unknownAny.typeUrl);
    expect(sent.backend.typedOptions.value).toEqual(unknownAny.value);
    expect(sent.backend.endpoint.value.host).toBe('web.local');
    expect(fixture.componentInstance.changes).toBe(1);
    expect(card('General').querySelector('button.edit')).not.toBeNull();
  });

  it('saves a switch to an empty oneof member and lists it in the dialog', async () => {
    const stored = create(BackendSchema, { ...storedBackend(), typedOptions: undefined });
    stored.healthCheck!.healthChecker = { case: undefined };
    backendApi['readBackend'].mockImplementation(async () => ({
      backend: create(BackendSchema, stored),
    }));
    const fixture = await render(create(BackendSchema, stored));

    button(card('Health checks'), 'Edit').click();
    await settle(fixture);
    card('Health checks')
      .querySelector<HTMLElement>('[data-oneof="health_checker"] [data-choice="noHealthCheck"]')!
      .click();
    await settle(fixture);
    button(card('Health checks'), 'Save').click();
    await settle(fixture);

    const confirmation = dialogs[0] as ChangeData;
    expect(confirmation.changes).toEqual([
      { path: 'health_check.no_health_check', before: undefined, after: '{}' },
    ]);
    const sent = backendApi['updateBackend'].mock.calls[0][0];
    expect(sent.backend.healthCheck.healthChecker.case).toBe('noHealthCheck');
  });

  it("hands a card to its kind's saver, which confirms on its own", async () => {
    const saver = vi.fn(async (_: Resource, edited: Resource) => edited);
    const fixture = await render(storedBackend(), 'backend', undefined, {
      saveCard: { General: saver },
    });

    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(card('General'), 'Save').click();
    await settle(fixture);

    expect(saver.mock.calls[0][1].name).toBe('api');
    expect(backendApi['updateBackend']).not.toHaveBeenCalled();
    expect(dialogs).toEqual([]);
    expect(fixture.componentInstance.changes).toBe(1);
  });

  it('shows what a failing saver stored before its error', async () => {
    backendApi['readBackend']
      .mockResolvedValueOnce({ backend: storedBackend() })
      .mockResolvedValueOnce({
        backend: create(BackendSchema, { ...storedBackend(), name: 'half' }),
      });
    const fixture = await render(storedBackend(), 'backend', undefined, {
      saveCard: {
        General: async () => {
          throw new Error('second rpc failed');
        },
      },
    });

    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(card('General'), 'Save').click();
    await settle(fixture);

    expect(panel().querySelector('.subject')!.textContent).toContain('half');
    expect(fixture.componentInstance.changes).toBe(1);
    expect(card('General').textContent).toContain('second rpc failed');
    expect(card('General').querySelector('button.save')).not.toBeNull();
  });

  it('keeps the card open when its saver ran nothing', async () => {
    const fixture = await render(storedBackend(), 'backend', undefined, {
      saveCard: { General: async () => null },
    });

    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(card('General'), 'Save').click();
    await settle(fixture);

    expect(card('General').querySelector('button.save')).not.toBeNull();
    expect(fixture.componentInstance.changes).toBe(0);
  });

  it('shows the problems of the kind rules on the card and saves nothing', async () => {
    const fixture = await render(storedBackend(), 'backend', undefined, {
      problems: () => [{ path: 'name', message: 'not this name' }],
    });

    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(card('General'), 'Save').click();
    await settle(fixture);

    expect(backendApi['updateBackend']).not.toHaveBeenCalled();
    expect(card('General').textContent).toContain('not this name');
  });

  it('runs the used-by lookup once per resource, not again after a save', async () => {
    const fixture = await render(storedBackend());

    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(card('General'), 'Save').click();
    await settle(fixture);

    expect(backendApi['updateBackend']).toHaveBeenCalled();
    expect(list).toHaveBeenCalledTimes(1);
  });

  it('closes and shows a resource of its used-by list in the table of its page', async () => {
    list.mockImplementation(async (kind: string) =>
      kind === 'vs' ? [create(VirtualServerSchema, { UUID: 'v1', name: 'web-https' })] : [],
    );
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    const fixture = await render(storedBackend());

    document.querySelector<HTMLButtonElement>('.used-by button.user')!.click();
    await settle(fixture);
    await new Promise((resolve) => setTimeout(resolve, 350));
    await settle(fixture);

    expect(fixture.componentInstance.closes).toBe(1);
    expect(navigate).toHaveBeenCalledWith(['/virtualservers'], { queryParams: { uuid: 'v1' } });
  });

  it('shows the reason to a reader clicking Edit and opens no form', async () => {
    check.mockResolvedValue({
      reason: 'You are a reader of web. Editing needs the writer role, ask an owner.',
    });
    const fixture = await render(storedBackend());

    button(card('General'), 'Edit').click();
    await settle(fixture);

    const general = card('General');
    expect(general.textContent).toContain('You are a reader of web.');
    expect(general.querySelector('button.save')).toBeNull();
    const edit = general.querySelector<HTMLButtonElement>('button.edit')!;
    expect(edit.getAttribute('aria-disabled')).toBe('true');
    expect(tooltipOf(fixture, edit)).toContain('You are a reader of web.');
    expect(general.querySelector<HTMLInputElement>('input[name="name"]')!.disabled).toBe(true);
  });

  it('shows a concurrent change and offers to reload', async () => {
    backendApi['readBackend'].mockResolvedValue({
      backend: create(BackendSchema, { ...storedBackend(), sni: 'other.local' }),
    });
    const fixture = await render(storedBackend());

    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(card('General'), 'Save').click();
    await settle(fixture);

    const confirmation = dialogs[0] as ChangeData;
    expect(confirmation.title).toBe('Changed since you opened it');
    expect(confirmation.confirm).toBe('Reload');
    expect(confirmation.changes).toEqual([{ path: 'sni', after: '"other.local"' }]);
    expect(backendApi['updateBackend']).not.toHaveBeenCalled();
    expect(card('TLS').querySelector<HTMLInputElement>('input[name="sni"]')!.value).toBe(
      'other.local',
    );
  });

  it('opens the permissions panel of a locked resource', async () => {
    const locked = storedBackend();
    locked.metadata = { 'flock.autoprovision': 'true' };
    const fixture = await render(locked);

    const permissions = panel().querySelector<HTMLButtonElement>('button.permissions')!;
    expect(permissions.disabled).toBe(false);
    permissions.click();
    await settle(fixture);
    expect(document.body.querySelectorAll('flock-side-panel').length).toBe(2);
  });

  it('has no Permissions button for a zone, which keeps no permissions', async () => {
    await render(create(ZoneSchema, { UUID: 'z1', name: 'dc1' }), 'zone');

    expect(panel().querySelector('button.permissions')).toBeNull();
  });

  it('opens Create from a Clone with name, UUID and the lock key cleared', async () => {
    const locked = create(BackendSchema, {
      ...storedBackend(),
      metadata: { team: 'lb', 'flock.autoprovision': 'true' },
    });
    let prefill: Resource | null = null;
    const items = editMenuItems(locked, { clone: (p) => (prefill = p), copy: () => undefined }, '');

    items.find((item) => item.label === 'Clone')!.run();

    const cloned = prefill as unknown as Backend;
    expect(cloned.UUID).toBe('');
    expect(cloned.name).toBe('');
    expect(cloned.metadata).toEqual({ team: 'lb' });
    expect(locked.metadata['flock.autoprovision']).toBe('true');

    await render(null, 'backend', cloned);
    expect(panel().querySelector('.title')!.textContent).toContain('New Backend');
    expect(card('TLS').querySelector('button.edit')).toBeNull();
    expect(button(panel(), 'Create')).toBeDefined();
  });

  it('creates the resource after the rules checks', async () => {
    const fixture = await render(null, 'backend', storedBackend());

    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, '');
    await settle(fixture);
    button(panel(), 'Create').click();
    await settle(fixture);
    expect(backendApi['createBackend']).not.toHaveBeenCalled();
    expect(card('General').textContent).toContain('value length must be at least 1 characters');

    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(panel(), 'Create').click();
    await settle(fixture);
    expect(backendApi['createBackend'].mock.calls[0][0].backend.name).toBe('api');
    expect(fixture.componentInstance.changes).toBe(1);
  });

  it('creates a backend from an empty form with only a name and an endpoint', async () => {
    const fixture = await render(null, 'backend');

    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    card('Endpoints').querySelector<HTMLElement>('[data-choice="dns"]')!.click();
    await settle(fixture);
    type(card('Endpoints').querySelector<HTMLInputElement>('input[name="host"]')!, 'web.local');
    type(card('Endpoints').querySelector<HTMLInputElement>('input[name="port"]')!, '80');
    await settle(fixture);
    button(panel(), 'Create').click();
    await settle(fixture);

    const sent = backendApi['createBackend'].mock.calls[0][0].backend;
    expect(sent.name).toBe('api');
    expect(sent.endpoint.value.host).toBe('web.local');
    expect(sent.refreshRate).toBeUndefined();
  });

  it('downloads the draft as a YAML file only once it passes the checks of Create', async () => {
    const names: string[] = [];
    // only the blob functions are replaced, the router of the panel needs the URL constructor
    const blobUrls = { create: URL.createObjectURL, revoke: URL.revokeObjectURL };
    URL.createObjectURL = () => 'blob:x';
    URL.revokeObjectURL = () => undefined;
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      names.push(this.download);
    });
    // a clone of a resource the dashboard knows whole, the fixture's own extension is unknown to it
    const prefill = create(BackendSchema, {
      ...storedBackend(),
      UUID: '',
      typedOptions: undefined,
    });
    const fixture = await render(null, 'backend', prefill);

    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, '');
    await settle(fixture);
    button(panel(), 'Download YAML').click();
    await settle(fixture);
    expect(names).toEqual([]);
    expect(card('General').textContent).toContain('value length must be at least 1 characters');

    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(panel(), 'Download YAML').click();
    await settle(fixture);
    click.mockRestore();
    URL.createObjectURL = blobUrls.create;
    URL.revokeObjectURL = blobUrls.revoke;

    expect(names).toEqual(['api.yml']);
    expect(backendApi['createBackend']).not.toHaveBeenCalled();
  });

  it('refuses a Create holding the lock key', async () => {
    const prefill = create(BackendSchema, {
      ...storedBackend(),
      UUID: '',
      metadata: { 'flock.autoprovision': 'true' },
    });
    const fixture = await render(null, 'backend', prefill);

    button(panel(), 'Create').click();
    await settle(fixture);

    expect(backendApi['createBackend']).not.toHaveBeenCalled();
    expect(card('Metadata').textContent).toContain(lockKeyRefusal);
  });

  it('refuses adding the lock key in the metadata card', async () => {
    const fixture = await render(storedBackend());

    button(card('Metadata'), 'Edit').click();
    await settle(fixture);
    button(card('Metadata'), 'Add').click();
    await settle(fixture);
    const inputs = card('Metadata').querySelectorAll<HTMLInputElement>('.entry input');
    type(inputs[2], 'flock.autoprovision');
    await settle(fixture);
    type(card('Metadata').querySelectorAll<HTMLInputElement>('.entry input')[3], 'true');
    await settle(fixture);
    button(card('Metadata'), 'Save').click();
    await settle(fixture);

    expect(backendApi['updateBackend']).not.toHaveBeenCalled();
    expect(card('Metadata').textContent).toContain(lockKeyRefusal);
  });

  it('greys Edit of a locked resource with the lock text', async () => {
    const locked = create(BackendSchema, {
      ...storedBackend(),
      metadata: { 'flock.autoprovision': 'true' },
    });
    const fixture = await render(locked);

    expect(panel().querySelector('.lock')).not.toBeNull();
    const edits = panel().querySelectorAll<HTMLButtonElement>('button.edit');
    expect(edits.length).toBe(7);
    for (const edit of edits) {
      expect(edit.getAttribute('aria-disabled')).toBe('true');
      expect(tooltipOf(fixture, edit)).toBe(lockReason);
    }
    edits[0].click();
    await settle(fixture);
    expect(check).not.toHaveBeenCalled();
  });

  it('greys an admin-only change from the session', async () => {
    const fixture = await render(create(ZoneSchema, { UUID: 'z1', name: 'dc1' }), 'zone');

    const edit = card('General').querySelector<HTMLButtonElement>('button.edit')!;
    expect(tooltipOf(fixture, edit)).toBe('Only an admin of the controller can update a zone.');
  });

  it('asks the browser to warn before a reload while a card is dirty', async () => {
    const unload = () => {
      const event = new Event('beforeunload', { cancelable: true });
      window.dispatchEvent(event);
      return event.defaultPrevented;
    };
    const fixture = await render(storedBackend());
    expect(unload()).toBe(false);

    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    expect(unload()).toBe(true);

    fixture.destroy();
    expect(unload()).toBe(false);
  });

  it('asks before dropping the changes of a card', async () => {
    const fixture = await render(storedBackend());
    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    answer = false;

    panel().querySelector<HTMLButtonElement>('button.close')!.click();
    await settle(fixture);

    expect((dialogs[0] as { title: string }).title).toBe('Drop your changes?');
    expect(fixture.componentInstance.closes).toBe(0);
  });

  it('copies a virtual server as a resource file document', async () => {
    const vs = create(VirtualServerSchema, { UUID: 'v1', name: 'web-https', port: 443 });
    let copied = '';

    editMenuItems(vs, { clone: () => undefined, copy: (text) => (copied = text) }, '')
      .find((item) => item.label === 'Copy as resource file document')!
      .run();
    await vi.waitFor(() => expect(copied).not.toBe(''));

    expect(copied.startsWith('kind: vs\n')).toBe(true);
    expect(copied).toContain('name: web-https');
  });

  it('greys the resource file document of a backend holding an unknown extension', () => {
    const items = editMenuItems(
      storedBackend(),
      { clone: () => undefined, copy: () => undefined },
      '',
    );
    const copy = items.find((item) => item.label === 'Copy as resource file document')!;
    expect(copy.disabled).toContain("holds an extension this dashboard doesn't know");
  });

  it('has no resource file document for a certificate', () => {
    const items = editMenuItems(
      create(CertificateSchema, { name: 'c' }),
      { clone: () => undefined, copy: () => undefined },
      '',
    );
    expect(items.map((item) => item.label)).toEqual(['Clone']);
  });

  it('greys Clone with the reason Create is greyed', () => {
    const backend = storedBackend();
    const actions = { clone: () => undefined, copy: () => undefined };
    const clone = (reason: string) =>
      editMenuItems(backend, actions, reason).find((item) => item.label === 'Clone')!;

    expect(clone('No create for you.').disabled).toBe('No create for you.');
    expect(clone('').disabled).toBeUndefined();
  });
});
