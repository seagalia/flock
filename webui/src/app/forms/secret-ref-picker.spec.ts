import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { provideRouter, Router } from '@angular/router';
import { create, DescMessage, Message } from '@bufbuild/protobuf';
import { of } from 'rxjs';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { SecretSchema } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { Api } from '../core/api';
import { ResourceCreations } from '../core/resource-creations';
import { Session } from '../core/session';
import { Access } from '../edit/access';
import { SidePanels } from '../ui/side-panel';
import { ProtoForm } from './proto-form';
import { SecretRefPicker } from './secret-ref-picker';

const dbUuid = '11111111-2222-3333-4444-555555555555';
const apiUuid = '66666666-7777-8888-9999-000000000000';
// the list answer gives the keys of a secret and leaves its values empty
const secrets = [
  create(SecretSchema, { UUID: dbUuid, name: 'db', data: { user: '', password: '' } }),
  create(SecretSchema, { UUID: apiUuid, name: 'dns-api', data: { token: '' } }),
];
let api: { list: ReturnType<typeof vi.fn> };
let creations: { start: ReturnType<typeof vi.fn> };

@Component({
  imports: [SecretRefPicker],
  template: `<flock-secret-ref-picker
    [value]="value()"
    (picked)="picked.set($event)"
    (cleared)="cleared.set(true)"
  />`,
})
class PickerHost {
  value = signal('');
  picked = signal('');
  cleared = signal(false);
}

@Component({
  imports: [ProtoForm],
  template: `<flock-proto-form [schema]="schema()" [(value)]="value" [allReadonly]="readOnly()" />`,
})
class FormHost {
  schema = signal<DescMessage>(BackendSchema);
  value = signal<Message>(create(BackendSchema));
  readOnly = signal(false);
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

function option(text: string): HTMLElement {
  const found = openOptions().find((o) => o.textContent?.includes(text));
  if (!found) {
    throw new Error(`no option ${text}`);
  }
  return found;
}

function optionTexts(): string[] {
  return openOptions().map((o) => (o.textContent ?? '').trim());
}

function select(root: HTMLElement, label: string): HTMLElement {
  const picker = root.querySelector('flock-secret-ref-picker') ?? root;
  const found = [...picker.querySelectorAll<HTMLElement>('mat-form-field')].find((f) =>
    f.textContent?.includes(label),
  );
  return found!.querySelector<HTMLElement>('.mat-mdc-select-trigger')!;
}

describe('SecretRefPicker', () => {
  beforeEach(() => {
    api = { list: vi.fn(async () => secrets) };
    creations = { start: vi.fn() };
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: Api, useValue: api },
        { provide: ResourceCreations, useValue: creations },
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

  it('lists the secrets the list answer returns, then the keys of the chosen one', async () => {
    const fixture = TestBed.createComponent(PickerHost);
    await settle(fixture);
    const root = fixture.nativeElement as HTMLElement;

    select(root, 'Secret').click();
    await settle(fixture);
    expect(api.list).toHaveBeenCalledWith('secret', expect.objectContaining({ offset: 0 }));
    expect(optionTexts().slice(1)).toEqual(['db', 'dns-api']);

    option('db').click();
    await settle(fixture);
    select(root, 'Key').click();
    await settle(fixture);
    expect(optionTexts()).toEqual(['user', 'password']);
  });

  it('writes the reference of the chosen key', async () => {
    const fixture = TestBed.createComponent(PickerHost);
    await settle(fixture);
    const root = fixture.nativeElement as HTMLElement;

    select(root, 'Secret').click();
    await settle(fixture);
    option('dns-api').click();
    await settle(fixture);
    select(root, 'Key').click();
    await settle(fixture);
    option('token').click();
    await settle(fixture);

    expect(fixture.componentInstance.picked()).toBe(`secret://${apiUuid}/token`);
  });

  it('shows the secret and the key of the reference it holds', async () => {
    const fixture = TestBed.createComponent(PickerHost);
    fixture.componentInstance.value.set(`secret://${apiUuid}/token`);
    await settle(fixture);
    const root = fixture.nativeElement as HTMLElement;

    expect(select(root, 'Secret').textContent).toContain('dns-api');
    expect(select(root, 'Key').textContent).toContain('token');
  });

  it('tells the entry its reference is gone once another secret is chosen', async () => {
    const fixture = TestBed.createComponent(PickerHost);
    fixture.componentInstance.value.set(`secret://${apiUuid}/token`);
    await settle(fixture);
    const root = fixture.nativeElement as HTMLElement;

    select(root, 'Secret').click();
    await settle(fixture);
    option('db').click();
    await settle(fixture);

    expect(fixture.componentInstance.cleared()).toBe(true);
    expect(select(root, 'Key').textContent).not.toContain('token');
  });

  it('offers to create a secret first, then picks the key of the created one', async () => {
    const fixture = TestBed.createComponent(PickerHost);
    await settle(fixture);
    const root = fixture.nativeElement as HTMLElement;

    select(root, 'Secret').click();
    await settle(fixture);
    expect(optionTexts()[0]).toContain('Create a secret');
    openOptions()[0].click();
    await settle(fixture);

    expect(creations.start).toHaveBeenCalledWith(expect.objectContaining({ kind: 'secret' }));
    const created = create(SecretSchema, {
      UUID: 'new-uuid',
      name: 'vigie',
      data: { password: 'p' },
    });
    creations.start.mock.calls[0][0].created(created);
    await settle(fixture);

    expect(select(root, 'Secret').textContent).toContain('vigie');
    expect(fixture.componentInstance.picked()).toBe('secret://new-uuid/password');
  });

  it('leaves the choice to the user when the created secret has several keys', async () => {
    const fixture = TestBed.createComponent(PickerHost);
    await settle(fixture);
    const root = fixture.nativeElement as HTMLElement;

    select(root, 'Secret').click();
    await settle(fixture);
    openOptions()[0].click();
    await settle(fixture);
    const created = create(SecretSchema, {
      UUID: 'new-uuid',
      name: 'creds',
      data: { user: 'u', password: 'p' },
    });
    creations.start.mock.calls[0][0].created(created);
    await settle(fixture);

    expect(select(root, 'Secret').textContent).toContain('creds');
    expect(fixture.componentInstance.picked()).toBe('');
    select(root, 'Key').click();
    await settle(fixture);
    expect(optionTexts()).toEqual(['user', 'password']);
  });

  it('keeps showing the secret it had when the creation is left', async () => {
    const fixture = TestBed.createComponent(PickerHost);
    fixture.componentInstance.value.set(`secret://${apiUuid}/token`);
    await settle(fixture);
    const root = fixture.nativeElement as HTMLElement;

    select(root, 'Secret').click();
    await settle(fixture);
    openOptions()[0].click();
    await settle(fixture);

    expect(select(root, 'Secret').textContent).toContain('dns-api');
    expect(fixture.componentInstance.cleared()).toBe(false);
  });

  it('never shows a value, the options only name secrets and keys', async () => {
    api.list.mockResolvedValue([
      create(SecretSchema, { UUID: dbUuid, name: 'db', data: { password: 'hunter2' } }),
    ]);
    const fixture = TestBed.createComponent(PickerHost);
    await settle(fixture);
    const root = fixture.nativeElement as HTMLElement;

    select(root, 'Secret').click();
    await settle(fixture);
    option('db').click();
    await settle(fixture);
    select(root, 'Key').click();
    await settle(fixture);

    expect(document.body.textContent).not.toContain('hunter2');
  });
});

describe('metadata of a resource', () => {
  beforeEach(() => {
    api = { list: vi.fn(async () => secrets) };
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: Api, useValue: api },
        { provide: ResourceCreations, useValue: { start: () => undefined } },
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
    document.querySelectorAll('.cdk-overlay-container').forEach((e) => e.remove());
  });

  async function render(schema: DescMessage, value: Message) {
    const fixture = TestBed.createComponent(FormHost);
    fixture.componentInstance.schema.set(schema);
    fixture.componentInstance.value.set(value);
    await settle(fixture);
    return fixture;
  }

  function metadata(fixture: { nativeElement: HTMLElement }) {
    return fixture.nativeElement.querySelector('[data-field="metadata"]')!;
  }

  function toggle(fixture: { nativeElement: HTMLElement }, kind: 'Text' | 'Secret') {
    const buttons = [
      ...metadata(fixture).querySelectorAll<HTMLElement>('mat-button-toggle button'),
    ];
    buttons.find((b) => b.textContent?.trim() === kind)!.click();
  }

  function stored(fixture: { componentInstance: FormHost }) {
    return (fixture.componentInstance.value() as unknown as { metadata: object }).metadata;
  }

  it('switches an entry to Secret and stores the picked reference', async () => {
    const fixture = await render(
      BackendSchema,
      create(BackendSchema, { name: 'web', metadata: { token: 'plain' } }),
    );
    const root = fixture.nativeElement as HTMLElement;

    toggle(fixture, 'Secret');
    await settle(fixture);
    expect(stored(fixture)).toEqual({ token: 'plain' });
    expect(metadata(fixture).textContent).toContain('an entry has no secret key picked');

    select(root, 'Secret').click();
    await settle(fixture);
    option('dns-api').click();
    await settle(fixture);
    select(root, 'Key').click();
    await settle(fixture);
    option('token').click();
    await settle(fixture);

    expect(stored(fixture)).toEqual({ token: `secret://${apiUuid}/token` });
    expect(metadata(fixture).textContent).not.toContain('no secret key picked');
  });

  it('opens a stored reference as Secret, never showing the raw reference', async () => {
    const fixture = await render(
      BackendSchema,
      create(BackendSchema, { name: 'web', metadata: { token: `secret://${apiUuid}/token` } }),
    );
    const root = fixture.nativeElement as HTMLElement;

    expect(select(root, 'Secret').textContent).toContain('dns-api');
    expect(select(root, 'Key').textContent).toContain('token');
    const values = [...metadata(fixture).querySelectorAll<HTMLInputElement>('input')].map(
      (i) => i.value,
    );
    expect(values).not.toContain(`secret://${apiUuid}/token`);
  });

  it('switches back to Text with an empty value', async () => {
    const fixture = await render(
      BackendSchema,
      create(BackendSchema, { name: 'web', metadata: { token: `secret://${apiUuid}/token` } }),
    );

    toggle(fixture, 'Text');
    await settle(fixture);

    expect(stored(fixture)).toEqual({ token: '' });
    expect(metadata(fixture).querySelector('flock-secret-ref-picker')).toBeNull();
  });

  it('shows a reference read only as the secret name and key, linking to the secret', async () => {
    const fixture = await render(
      BackendSchema,
      create(BackendSchema, { name: 'web', metadata: { token: `secret://${apiUuid}/token` } }),
    );
    fixture.componentInstance.readOnly.set(true);
    await settle(fixture);

    expect(metadata(fixture).querySelector('mat-button-toggle-group')).toBeNull();
    const link = metadata(fixture).querySelector<HTMLAnchorElement>('a')!;
    expect(link.textContent?.trim()).toBe('dns-api / token');
    const router = TestBed.inject(Router);
    expect(router.serializeUrl(router.parseUrl(link.getAttribute('href')!))).toBe(
      `/secrets?uuid=${apiUuid}`,
    );
  });

  it('keeps the input under the cursor while a reference is typed by hand in a Text entry', async () => {
    const fixture = await render(
      BackendSchema,
      create(BackendSchema, { name: 'web', metadata: { token: '' } }),
    );
    const valueInput = () =>
      [...metadata(fixture).querySelectorAll<HTMLInputElement>('input')].find((i) =>
        i.closest('mat-form-field')?.textContent?.includes('Value'),
      )!;
    const first = valueInput();
    const reference = `secret://${apiUuid}/token`;

    for (let end = 1; end <= reference.length; end++) {
      first.value = reference.slice(0, end);
      first.dispatchEvent(new Event('input'));
      await settle(fixture);
      expect(valueInput()).toBe(first);
      expect(valueInput().value).toBe(reference.slice(0, end));
    }
    expect(stored(fixture)).toEqual({ token: reference });
  });

  it('does not navigate when an open panel refuses to close', async () => {
    const fixture = await render(
      BackendSchema,
      create(BackendSchema, { name: 'web', metadata: { token: `secret://${apiUuid}/token` } }),
    );
    fixture.componentInstance.readOnly.set(true);
    await settle(fixture);
    vi.spyOn(TestBed.inject(SidePanels), 'closeAbove').mockResolvedValue(false);
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigate');

    metadata(fixture).querySelector<HTMLAnchorElement>('a')!.click();
    await settle(fixture);

    expect(navigate).not.toHaveBeenCalled();
  });

  it('shows a malformed reference as plain text, the controller refuses it on save', async () => {
    const fixture = await render(
      BackendSchema,
      create(BackendSchema, { name: 'web', metadata: { token: `secret://${apiUuid}` } }),
    );

    expect(metadata(fixture).querySelector('a')).toBeNull();
    const inputs = [...metadata(fixture).querySelectorAll<HTMLInputElement>('input')];
    expect(inputs.map((i) => i.value)).toContain(`secret://${apiUuid}`);
  });

  it('does not offer the picker in the metadata of a secret', async () => {
    const fixture = await render(
      SecretSchema,
      create(SecretSchema, { name: 'db', data: { user: '' }, metadata: { team: 'lb' } }),
    );

    expect(metadata(fixture).querySelector('mat-button-toggle-group')).toBeNull();
  });
});
