import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import {
  MemberPermission,
  MemberPermissionSchema,
  MemberType,
  Role,
} from '../../gen/flock/api/config/permission/v1/permission_pb';
import { Api } from '../core/api';
import { Access } from './access';
import { PermissionsPanel } from './permissions-panel';

@Component({
  imports: [PermissionsPanel],
  template: `
    <flock-permissions-panel
      [resource]="resource"
      (closed)="closes = closes + 1"
      (changed)="changes = changes + 1"
    />
  `,
})
class Host {
  resource = create(BackendSchema, { UUID: 'b1', name: 'web' });
  closes = 0;
  changes = 0;
}

function member(name: string, type: MemberType, role: Role): MemberPermission {
  return create(MemberPermissionSchema, { member: { memberName: name, memberType: type }, role });
}

let permissionApi: Record<string, ReturnType<typeof vi.fn>>;
let check: ReturnType<typeof vi.fn>;

function setup() {
  permissionApi = {
    listMemberPermissions: vi.fn(async () => ({
      memberPermissions: [
        member('alice', MemberType.USER, Role.OWNER),
        member('devs', MemberType.GROUP, Role.WRITER),
      ],
      allowAllRead: true,
    })),
    addPermission: vi.fn(async () => ({})),
    deletePermission: vi.fn(async () => ({})),
    setAllowAllRead: vi.fn(async () => ({})),
  };
  check = vi.fn(async () => null);
  TestBed.configureTestingModule({
    providers: [
      { provide: Api, useValue: { permission: permissionApi } },
      { provide: Access, useValue: { check } },
    ],
  });
}

async function settle(fixture: { whenStable(): Promise<unknown> }) {
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
}

async function render() {
  const fixture = TestBed.createComponent(Host);
  await settle(fixture);
  return fixture;
}

function panel(): HTMLElement {
  return document.body.querySelector('flock-side-panel')!;
}

// opens a mat-select and clicks the option of the given label, the way a user does
async function pick(fixture: { whenStable(): Promise<unknown> }, select: Element, label: string) {
  select.querySelector<HTMLElement>('.mat-mdc-select-trigger')!.click();
  await settle(fixture);
  const option = [...document.querySelectorAll<HTMLElement>('mat-option')].find(
    (o) => o.textContent?.trim() === label,
  )!;
  option.click();
  await settle(fixture);
}

describe('PermissionsPanel', () => {
  beforeEach(setup);
  afterEach(() =>
    document.body
      .querySelectorAll('flock-side-panel, .cdk-overlay-container')
      .forEach((p) => p.remove()),
  );

  it('lists the members by role and the allow-all-read switch', async () => {
    await render();
    const owners = panel().querySelector('[data-role="Owner"]')!;
    const writers = panel().querySelector('[data-role="Writer"]')!;
    expect(owners.textContent).toContain('alice');
    expect(writers.textContent).toContain('devs');
    expect(permissionApi['listMemberPermissions']).toHaveBeenCalledWith(
      {
        resourceType: 'type.googleapis.com/flock.api.config.backend.v1.Backend',
        resourceUuid: 'b1',
        seeAll: true,
      },
      expect.anything(),
    );
    const toggle = panel().querySelector<HTMLButtonElement>('button[role="switch"]')!;
    expect(toggle.getAttribute('aria-checked')).toBe('true');
  });

  it('greys every change control with the reason when the caller is no owner', async () => {
    check.mockResolvedValue({ reason: 'You are a reader of web.' });
    await render();
    panel().querySelector<HTMLButtonElement>('button[aria-label="Remove devs"]')!.click();
    await settle({ whenStable: async () => undefined });
    expect(check).toHaveBeenCalledWith(
      expect.anything(),
      Role.OWNER,
      expect.anything(),
      'Changing permissions',
    );
    expect(permissionApi['deletePermission']).not.toHaveBeenCalled();
    expect(panel().textContent).toContain('You are a reader of web.');
    const controls = panel().querySelectorAll<HTMLButtonElement>(
      'button[aria-label^="Remove"], button.add',
    );
    expect([...controls].every((c) => c.disabled)).toBe(true);
  });

  it('puts the role select back to the stored role after a denial', async () => {
    check.mockResolvedValue({ reason: 'You are a reader of web.' });
    const fixture = await render();
    const select = panel().querySelector('mat-select[data-member="devs"]')!;
    await pick(fixture, select, 'Reader');
    expect(permissionApi['addPermission']).not.toHaveBeenCalled();
    expect(select.textContent).toContain('Writer');
    expect(select.textContent).not.toContain('Reader');
  });

  it('puts the switch back to the stored state after a denial', async () => {
    check.mockResolvedValue({ reason: 'You are a reader of web.' });
    const fixture = await render();
    const toggle = panel().querySelector<HTMLButtonElement>('button[role="switch"]')!;
    toggle.click();
    await settle(fixture);
    expect(permissionApi['setAllowAllRead']).not.toHaveBeenCalled();
    expect(toggle.getAttribute('aria-checked')).toBe('true');
  });

  it('shows a failed first read inline', async () => {
    permissionApi['listMemberPermissions'].mockRejectedValue(
      new ConnectError('no such resource', Code.NotFound),
    );
    const fixture = await render();
    await settle(fixture);
    expect(panel().querySelector('[role="alert"]')!.textContent).toContain('no such resource');
  });

  it('removes a member and lists again', async () => {
    await render();
    panel().querySelector<HTMLButtonElement>('button[aria-label="Remove devs"]')!.click();
    await new Promise((resolve) => setTimeout(resolve));
    expect(permissionApi['deletePermission']).toHaveBeenCalledWith(
      {
        resourceType: 'type.googleapis.com/flock.api.config.backend.v1.Backend',
        resourceUuid: 'b1',
        member: expect.objectContaining({ memberName: 'devs', memberType: MemberType.GROUP }),
      },
      expect.anything(),
    );
    expect(permissionApi['listMemberPermissions']).toHaveBeenCalledTimes(2);
  });

  it('tells the page a change went through, so it lists the permissions again', async () => {
    const fixture = await render();
    panel().querySelector<HTMLButtonElement>('button[aria-label="Remove devs"]')!.click();
    await new Promise((resolve) => setTimeout(resolve));

    expect(fixture.componentInstance.changes).toBe(1);
  });

  it('shows the refusal of the controller when the last owner is removed', async () => {
    permissionApi['deletePermission'].mockRejectedValue(
      new ConnectError('the last owner can not be removed', Code.FailedPrecondition),
    );
    const fixture = await render();
    panel().querySelector<HTMLButtonElement>('button[aria-label="Remove alice"]')!.click();
    await new Promise((resolve) => setTimeout(resolve));
    await settle({ whenStable: async () => undefined });
    expect(panel().querySelector('[role="alert"]')!.textContent).toContain(
      'the last owner can not be removed',
    );
    expect(fixture.componentInstance.changes).toBe(0);
  });

  it('adds a member with a role', async () => {
    const fixture = await render();
    const name = panel().querySelector<HTMLInputElement>('input.member-name')!;
    name.value = 'bob';
    name.dispatchEvent(new Event('input'));
    await settle(fixture);
    await pick(fixture, panel().querySelector('mat-select.member-role')!, 'Writer');
    panel().querySelector<HTMLButtonElement>('button.add')!.click();
    await new Promise((resolve) => setTimeout(resolve));
    expect(permissionApi['addPermission']).toHaveBeenCalledWith(
      {
        resourceType: 'type.googleapis.com/flock.api.config.backend.v1.Backend',
        resourceUuid: 'b1',
        Role: Role.WRITER,
        member: expect.objectContaining({ memberName: 'bob', memberType: MemberType.USER }),
      },
      expect.anything(),
    );
  });

  it('changes the role of a member', async () => {
    const fixture = TestBed.createComponent(Host);
    await settle(fixture);
    await pick(fixture, panel().querySelector('mat-select[data-member="devs"]')!, 'Reader');
    expect(permissionApi['addPermission']).toHaveBeenCalledWith(
      {
        resourceType: 'type.googleapis.com/flock.api.config.backend.v1.Backend',
        resourceUuid: 'b1',
        Role: Role.READER,
        member: expect.objectContaining({ memberName: 'devs', memberType: MemberType.GROUP }),
      },
      expect.anything(),
    );
  });

  it('sets allow-all-read', async () => {
    await render();
    panel().querySelector<HTMLButtonElement>('button[role="switch"]')!.click();
    await new Promise((resolve) => setTimeout(resolve));
    expect(permissionApi['setAllowAllRead']).toHaveBeenCalledWith(
      {
        resourceType: 'type.googleapis.com/flock.api.config.backend.v1.Backend',
        resourceUuid: 'b1',
        allowAllRead: false,
      },
      expect.anything(),
    );
  });
});
