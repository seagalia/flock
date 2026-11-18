import { Component, viewChild } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { MatTooltip } from '@angular/material/tooltip';
import { By } from '@angular/platform-browser';
import { provideRouter } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { Resource } from '../core/resources';
import { Backend, BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { ZoneSchema } from '../../gen/flock/api/config/zone/v1/zone_pb';
import { of } from 'rxjs';
import { Role } from '../../gen/flock/api/config/permission/v1/permission_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api } from '../core/api';
import { isProposedName } from '../core/names';
import { Session } from '../core/session';
import { Access } from './access';
import { LockBadge } from './lock-badge';
import { lockReason } from './lock';
import { ResourceEditor } from './resource-editor';
import { ResourcePanel } from './resource-panel';
import { VipAttach } from './vip-attach';

@Component({
  imports: [ResourceEditor, LockBadge],
  template: `
    <flock-resource-editor [kind]="kind" (changed)="changes = changes + 1" />
    <flock-lock-badge [resource]="locked" />
  `,
})
class Host {
  kind: 'backend' | 'zone' | 'vip' | 'vs' = 'backend';
  changes = 0;
  locked = create(BackendSchema, { name: 'auto', metadata: { 'flock.autoprovision': 'true' } });
  editor = viewChild.required(ResourceEditor);
}

let list: ReturnType<typeof vi.fn>;
let check: ReturnType<typeof vi.fn>;
let deleteBackend: ReturnType<typeof vi.fn>;
let editVips: ReturnType<typeof vi.fn>;
let dialogs: { component: unknown; data: { title: string; message: string } }[];
let isAdmin: boolean;

@Component({
  imports: [ResourceEditor],
  template: `
    <flock-resource-editor
      kind="backend"
      [linking]="true"
      (created)="created.push($event)"
      (finished)="finished = finished + 1"
    />
  `,
})
class LinkingHost {
  created: Resource[] = [];
  finished = 0;
}

function setup() {
  list = vi.fn(async () => []);
  check = vi.fn(async () => null);
  deleteBackend = vi.fn(async () => ({}));
  editVips = vi.fn(async () => true);
  dialogs = [];
  isAdmin = false;
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      {
        provide: Api,
        useValue: {
          list,
          backend: {
            deleteBackend,
            createBackend: vi.fn(async (req: { backend: Backend }) => ({
              backend: { ...req.backend, UUID: 'backend-new' },
            })),
          },
        },
      },
      {
        provide: Access,
        useValue: { check, features: async () => ({ ipamEnabled: false }) },
      },
      { provide: VipAttach, useValue: { edit: editVips } },
      {
        provide: Session,
        useValue: { user: () => ({ username: 'alice', groups: [], isAdmin }) },
      },
      {
        provide: MatDialog,
        useValue: {
          open: (component: unknown, config: { data: { title: string; message: string } }) => {
            dialogs.push({ component, data: config.data });
            return { afterClosed: () => of(true) };
          },
        },
      },
    ],
  });
}

function menuItem(
  fixture: ReturnType<typeof TestBed.createComponent<Host>>,
  row: Resource,
  label: string,
) {
  return fixture.componentInstance
    .editor()
    .menuItems(row)
    .find((item) => item.label === label)!;
}

async function settle(fixture: { whenStable(): Promise<unknown> }) {
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
}

function tooltipOf(fixture: ReturnType<typeof TestBed.createComponent>, element: Element) {
  const found = fixture.debugElement
    .queryAll(By.directive(MatTooltip))
    .find((d) => d.nativeElement === element);
  return found?.injector.get(MatTooltip).message;
}

describe('ResourceEditor', () => {
  beforeEach(setup);

  afterEach(() => {
    document
      .querySelectorAll('flock-side-panel, .cdk-overlay-container')
      .forEach((e) => e.remove());
  });

  it('greys Create of an admin-only kind for a non-admin', async () => {
    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.kind = 'zone';
    await settle(fixture);

    const createButton = fixture.nativeElement.querySelector('button.create') as HTMLButtonElement;
    expect(createButton.getAttribute('aria-disabled')).toBe('true');
    expect(tooltipOf(fixture, createButton)).toBe(
      'Only an admin of the controller can create a zone.',
    );
    createButton.click();
    await settle(fixture);
    expect(document.body.querySelector('flock-side-panel')).toBeNull();
  });

  it('names a VIP with its capitals in the admin reason and greys Clone with it', async () => {
    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.kind = 'vip';
    await settle(fixture);

    const createButton = fixture.nativeElement.querySelector('button.create') as HTMLButtonElement;
    const reason = 'Only an admin of the controller can create a VIP.';
    expect(tooltipOf(fixture, createButton)).toBe(reason);
    const stored = create(VIPSchema, { UUID: 'v1', name: 'web-vip' });
    const clone = fixture.componentInstance
      .editor()
      .menuItems(stored)
      .find((item) => item.label === 'Clone')!;
    expect(clone.disabled).toBe(reason);
  });

  it('opens a resource in the panel', async () => {
    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.kind = 'zone';
    await settle(fixture);

    fixture.componentInstance.editor().open(create(ZoneSchema, { UUID: 'z1', name: 'dc1' }));
    await settle(fixture);

    const panel = document.body.querySelector('flock-side-panel')!;
    expect(panel.textContent).toContain('dc1');
    expect(panel.querySelector('[data-card="Pools"]')).not.toBeNull();
  });

  it('opens Create prefilled from the Clone of a row', async () => {
    const fixture = TestBed.createComponent(Host);
    await settle(fixture);
    const stored = create(BackendSchema, { UUID: 'b1', name: 'web', sni: 'web.local' });

    const items = fixture.componentInstance.editor().menuItems(stored);
    items.find((item) => item.label === 'Clone')!.run();
    await settle(fixture);

    const panel = document.body.querySelector('flock-side-panel')!;
    expect(panel.querySelector('.title')!.textContent).toContain('New Backend');
    expect(panel.querySelector<HTMLInputElement>('input[name="sni"]')!.value).toBe('web.local');
    const name = panel.querySelector<HTMLInputElement>('input[name="name"]')!.value;
    expect(isProposedName(name)).toBe(true);
  });

  it('shows the lock badge of a locked resource', async () => {
    const fixture = TestBed.createComponent(Host);
    await settle(fixture);

    const badge = fixture.nativeElement.querySelector('flock-lock-badge mat-icon');
    expect(badge.textContent).toContain('lock');
    expect(tooltipOf(fixture, badge)).toBe(lockReason);
  });
  it('opens a row in the panel from Edit, even a locked one', async () => {
    const fixture = TestBed.createComponent(Host);
    await settle(fixture);
    const stored = create(BackendSchema, {
      UUID: 'b1',
      name: 'auto',
      metadata: { 'flock.autoprovision': 'true' },
    });

    const edit = menuItem(fixture, stored, 'Edit');
    expect(edit.tone).toBe('primary');
    expect(edit.disabled).toBeUndefined();
    edit.run();
    await settle(fixture);

    expect(document.body.querySelector('flock-side-panel')!.textContent).toContain('auto');
  });

  it('greys Delete of a locked resource for a non-admin, saying only an admin can force it', async () => {
    const fixture = TestBed.createComponent(Host);
    await settle(fixture);

    const remove = menuItem(fixture, fixture.componentInstance.locked, 'Delete');
    expect(remove.tone).toBe('danger');
    expect(remove.disabled).toBe(`${lockReason} Only an admin can delete it anyway.`);
  });

  it('lets an admin delete a locked resource once its name is typed in the confirmation', async () => {
    isAdmin = true;
    const fixture = TestBed.createComponent(Host);
    await settle(fixture);
    const locked = create(BackendSchema, {
      UUID: 'b1',
      name: 'auto',
      metadata: { 'flock.autoprovision': 'true' },
    });

    const remove = menuItem(fixture, locked, 'Delete');
    expect(remove.disabled).toBeUndefined();
    remove.run();
    await settle(fixture);

    expect(check.mock.calls[0][1]).toBe(Role.OWNER);
    expect(dialogs[0].data).toEqual(
      expect.objectContaining({ title: 'Delete the locked backend auto?', typed: 'auto' }),
    );
    expect(dialogs[0].data.message).toContain('may create it again');
    expect(deleteBackend).toHaveBeenCalledWith({ UUID: 'b1' });
  });

  it('refuses Delete of a resource something uses, before any permission call', async () => {
    list.mockImplementation(async (kind: string) =>
      kind === 'vs' ? [create(VirtualServerSchema, { UUID: 'v1', name: 'web-https' })] : [],
    );
    const fixture = TestBed.createComponent(Host);
    await settle(fixture);

    menuItem(fixture, create(BackendSchema, { UUID: 'b1', name: 'web' }), 'Delete').run();
    await settle(fixture);

    expect(dialogs[0].data).toEqual({
      title: "Can't delete the backend web",
      message: 'Used by:',
      links: [{ label: 'Virtual Server web-https', path: '/virtualservers', uuid: 'v1' }],
    });
    expect(check).not.toHaveBeenCalled();
    expect(deleteBackend).not.toHaveBeenCalled();
  });

  it('refuses Delete without the owner role, with the reason of the check', async () => {
    check.mockImplementation(async () => ({ reason: 'You are a reader of web.' }));
    const fixture = TestBed.createComponent(Host);
    await settle(fixture);

    menuItem(fixture, create(BackendSchema, { UUID: 'b1', name: 'web' }), 'Delete').run();
    await settle(fixture);

    expect(dialogs[0].data.message).toBe('You are a reader of web.');
    expect(deleteBackend).not.toHaveBeenCalled();
  });

  it('deletes after the owner check and a confirmation naming the resource', async () => {
    const fixture = TestBed.createComponent(Host);
    await settle(fixture);

    menuItem(fixture, create(BackendSchema, { UUID: 'b1', name: 'web' }), 'Delete').run();
    await settle(fixture);

    expect(check.mock.calls[0][1]).toBe(Role.OWNER);
    expect(dialogs[0].data.title).toBe('Delete the backend web?');
    expect(deleteBackend).toHaveBeenCalledWith({ UUID: 'b1' });
    expect(fixture.componentInstance.changes).toBe(1);
  });

  it('offers Attach to VIPs in the menu of a virtual server only', async () => {
    const backends = TestBed.createComponent(Host);
    await settle(backends);
    const backend = create(BackendSchema, { UUID: 'b1' });
    expect(menuItem(backends, backend, 'Attach to VIPs')).toBeUndefined();

    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.kind = 'vs';
    await settle(fixture);
    const web = create(VirtualServerSchema, { UUID: 'v1', name: 'web' });
    menuItem(fixture, web, 'Attach to VIPs').run();
    await settle(fixture);

    expect(editVips).toHaveBeenCalledWith(web, false);
    expect(fixture.componentInstance.changes).toBe(1);
  });

  it('proposes the VIPs right after the Create of a virtual server', async () => {
    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.kind = 'vs';
    await settle(fixture);
    fixture.nativeElement.querySelector('button.create').click();
    await settle(fixture);

    const web = create(VirtualServerSchema, { UUID: 'v1', name: 'web' });
    fixture.debugElement.query(By.directive(ResourcePanel)).componentInstance.created.emit(web);
    await settle(fixture);

    expect(editVips).toHaveBeenCalledWith(web, true);
  });

  it('opens its Create panel at once when linking, then hands the created resource and finishes', async () => {
    const fixture = TestBed.createComponent(LinkingHost);
    await settle(fixture);
    const panel = document.body.querySelector('flock-side-panel')!;
    expect(panel.querySelector('.title')!.textContent).toContain('New Backend');
    expect(fixture.nativeElement.querySelector('button.create')).toBeNull();

    const name = panel.querySelector<HTMLInputElement>('input[name="name"]')!;
    name.value = 'api';
    name.dispatchEvent(new Event('input'));
    await settle(fixture);
    panel.querySelector<HTMLElement>('[data-choice="dns"]')!.click();
    await settle(fixture);
    for (const [field, value] of [
      ['host', 'api.local'],
      ['port', '80'],
    ]) {
      const input = panel.querySelector<HTMLInputElement>(`input[name="${field}"]`)!;
      input.value = value;
      input.dispatchEvent(new Event('input'));
    }
    await settle(fixture);
    panel.querySelector<HTMLButtonElement>('button.create-button')!.click();
    await settle(fixture);
    await new Promise((resolve) => setTimeout(resolve, 350));

    expect(fixture.componentInstance.created.map((resource) => resource.UUID)).toEqual([
      'backend-new',
    ]);
    expect(fixture.componentInstance.finished).toBe(1);
    expect(document.body.querySelector('flock-side-panel')).toBeNull();
  });
});
