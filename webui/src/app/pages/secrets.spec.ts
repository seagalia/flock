import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';
import { create } from '@bufbuild/protobuf';
import { Secret, SecretSchema } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { Api } from '../core/api';
import { Session } from '../core/session';
import { Access } from '../edit/access';
import { ChangeData } from '../edit/confirm-change';
import { Secrets } from './secrets';

function storedSecret(): Secret {
  return create(SecretSchema, {
    UUID: 's1',
    name: 'dns-token',
    data: { API_TOKEN: '', ZONE_ID: '' },
  });
}

const revealed = create(SecretSchema, {
  UUID: 's1',
  name: 'dns-token',
  data: { API_TOKEN: 'cf-12345', ZONE_ID: 'z-9' },
});

let secretApi: Record<string, ReturnType<typeof vi.fn>>;
let check: ReturnType<typeof vi.fn>;
let dialogs: { data: unknown }[];

function setup() {
  dialogs = [];
  check = vi.fn(async () => null);
  secretApi = {
    readSecret: vi.fn(async () => ({ secret: storedSecret() })),
    revealSecret: vi.fn(async () => ({ secret: revealed })),
    updateSecret: vi.fn(async (req: { secret: Secret }) => ({ secret: req.secret })),
  };
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      {
        provide: Api,
        useValue: {
          secret: secretApi,
          list: vi.fn(async (kind: string) => (kind === 'secret' ? [storedSecret()] : [])),
          permission: { listMemberPermissions: async () => ({ memberPermissions: [] }) },
        },
      },
      { provide: Access, useValue: { check, features: async () => ({ ipamEnabled: false }) } },
      {
        provide: Session,
        useValue: { user: () => ({ username: 'alice', groups: [], isAdmin: false }) },
      },
      {
        provide: MatDialog,
        useValue: {
          open: (_component: unknown, config: { data: unknown }) => {
            dialogs.push(config);
            return { afterClosed: () => of(true) };
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

async function clickMenuItem(fixture: { whenStable(): Promise<unknown> }, label: string) {
  document.body.querySelector<HTMLElement>('flock-resource-menu button')!.click();
  await settle(fixture);
  const items = Array.from(document.body.querySelectorAll<HTMLElement>('[mat-menu-item]'));
  items.find((item) => item.textContent!.includes(label))!.click();
  await settle(fixture);
}

describe('Secrets', () => {
  beforeEach(setup);

  afterEach(() => {
    document
      .querySelectorAll('flock-side-panel, .cdk-overlay-container')
      .forEach((e) => e.remove());
  });

  it('lists the name and the keys of a secret and no value', async () => {
    // the controller blanks values, a leaked one must still never be drawn since the page only knows the keys
    const leaking = create(SecretSchema, {
      UUID: 's1',
      name: 'dns-token',
      data: { API_TOKEN: 'would-be-leaked', ZONE_ID: '' },
    });
    const api = TestBed.inject(Api) as unknown as { list: ReturnType<typeof vi.fn> };
    api.list.mockResolvedValue([leaking]);
    const fixture = TestBed.createComponent(Secrets);
    document.body.append(fixture.nativeElement);
    await settle(fixture);
    const page: HTMLElement = fixture.nativeElement;

    const row = page.querySelector('tr.mat-mdc-row')!;
    expect(row.textContent).toContain('dns-token');
    expect(row.textContent).toContain('API_TOKEN');
    expect(row.textContent).toContain('ZONE_ID');
    expect(page.textContent).not.toContain('would-be-leaked');
    fixture.nativeElement.remove();
  });

  it('shows each stored key with an empty password input and saves the keys with empty values', async () => {
    const fixture = TestBed.createComponent(Secrets);
    document.body.append(fixture.nativeElement);
    await settle(fixture);
    await clickMenuItem(fixture, 'Edit');
    const card = document.body.querySelector('[data-card="Data"]')!;
    card.querySelector<HTMLButtonElement>('button.edit')!.click();
    await settle(fixture);

    const rows = () => Array.from(card.querySelectorAll('.entry'));
    const valueOf = (row: Element) => row.querySelectorAll<HTMLInputElement>('input')[1];
    expect(rows().length).toBe(2);
    for (const row of rows()) {
      expect(valueOf(row).type).toBe('password');
      expect(valueOf(row).value).toBe('');
      expect(valueOf(row).placeholder).toBe('unchanged');
    }

    const add = Array.from(card.querySelectorAll('button')).find(
      (b) => b.textContent!.trim() === 'Add',
    )!;
    add.click();
    await settle(fixture);
    const added = rows()[2];
    expect(valueOf(added).placeholder).toBe('');
    const [key, value] = Array.from(added.querySelectorAll<HTMLInputElement>('input'));
    key.value = 'NEW_KEY';
    key.dispatchEvent(new Event('input'));
    value.value = 'fresh';
    value.dispatchEvent(new Event('input'));
    await settle(fixture);
    card.querySelector<HTMLButtonElement>('button.save')!.click();
    await settle(fixture);

    expect(secretApi['updateSecret']).toHaveBeenCalledOnce();
    const sent = secretApi['updateSecret'].mock.calls[0][0] as { UUID: string; secret: Secret };
    expect(sent.UUID).toBe('s1');
    expect(sent.secret.data).toEqual({ API_TOKEN: '', ZONE_ID: '', NEW_KEY: 'fresh' });
    const confirmation = dialogs.map((d) => d.data as ChangeData).find((d) => d.changes);
    expect(JSON.stringify(confirmation)).not.toContain('fresh');
    fixture.nativeElement.remove();
  });

  it('reveals the values in a side panel only after the click, checking the owner role', async () => {
    const fixture = TestBed.createComponent(Secrets);
    document.body.append(fixture.nativeElement);
    await settle(fixture);
    expect(secretApi['revealSecret']).not.toHaveBeenCalled();
    expect(document.body.textContent).not.toContain('cf-12345');

    await clickMenuItem(fixture, 'Reveal');

    expect(check).toHaveBeenCalled();
    expect(secretApi['revealSecret']).toHaveBeenCalledWith({ UUID: 's1' });
    const panel = document.body.querySelector('flock-side-panel')!;
    expect(panel.textContent).toContain('API_TOKEN');
    expect(panel.textContent).toContain('cf-12345');
    expect(panel.textContent).toContain('z-9');
    fixture.nativeElement.remove();
  });

  it('refuses to reveal to someone without the owner role and calls nothing', async () => {
    check.mockResolvedValue({ reason: 'You are a reader of dns-token.' });
    const fixture = TestBed.createComponent(Secrets);
    document.body.append(fixture.nativeElement);
    await settle(fixture);

    await clickMenuItem(fixture, 'Reveal');

    expect(secretApi['revealSecret']).not.toHaveBeenCalled();
    expect(JSON.stringify(dialogs)).toContain('You are a reader of dns-token.');
  });

  it('offers neither Clone nor a file of the secret', async () => {
    const fixture = TestBed.createComponent(Secrets);
    document.body.append(fixture.nativeElement);
    await settle(fixture);
    document.body.querySelector<HTMLElement>('flock-resource-menu button')!.click();
    await settle(fixture);

    const labels = Array.from(document.body.querySelectorAll('[mat-menu-item]')).map((i) =>
      i.textContent!.trim(),
    );
    expect(labels.some((label) => label.includes('Reveal'))).toBe(true);
    expect(labels.some((label) => label.includes('Clone'))).toBe(false);
    expect(labels.some((label) => label.includes('resource file'))).toBe(false);
    fixture.nativeElement.remove();
  });

  it('creates a secret with no Download YAML', async () => {
    const fixture = TestBed.createComponent(Secrets);
    document.body.append(fixture.nativeElement);
    await settle(fixture);
    fixture.nativeElement.querySelector('button.create').click();
    await settle(fixture);

    const panel = document.body.querySelector('flock-side-panel')!;
    expect(panel.querySelector('.create-button')).not.toBeNull();
    expect(panel.querySelector('.download-button')).toBeNull();
    fixture.nativeElement.remove();
  });

  it('keeps the typed value of a key named with a dash out of the confirmation', async () => {
    const fixture = TestBed.createComponent(Secrets);
    document.body.append(fixture.nativeElement);
    await settle(fixture);
    await clickMenuItem(fixture, 'Edit');
    const card = document.body.querySelector('[data-card="Data"]')!;
    card.querySelector<HTMLButtonElement>('button.edit')!.click();
    await settle(fixture);
    Array.from(card.querySelectorAll('button'))
      .find((b) => b.textContent!.trim() === 'Add')!
      .click();
    await settle(fixture);
    const [key, value] = Array.from(
      card.querySelectorAll('.entry')[2].querySelectorAll<HTMLInputElement>('input'),
    );
    key.value = 'my-token';
    key.dispatchEvent(new Event('input'));
    value.value = 'dash-secret-value';
    value.dispatchEvent(new Event('input'));
    await settle(fixture);
    card.querySelector<HTMLButtonElement>('button.save')!.click();
    await settle(fixture);

    const confirmation = dialogs.map((d) => d.data as ChangeData).find((d) => d.changes);
    expect(JSON.stringify(confirmation)).toContain('my-token');
    expect(JSON.stringify(confirmation)).not.toContain('dash-secret-value');
    fixture.nativeElement.remove();
  });

  it('drops the revealed values from the page once the reveal panel closes', async () => {
    const fixture = TestBed.createComponent(Secrets);
    document.body.append(fixture.nativeElement);
    await settle(fixture);
    await clickMenuItem(fixture, 'Reveal');
    expect(document.body.textContent).toContain('cf-12345');

    document.body.querySelector<HTMLElement>('flock-side-panel button.close')!.click();
    await new Promise((resolve) => setTimeout(resolve, 400));
    await settle(fixture);

    expect(document.body.textContent).not.toContain('cf-12345');
    fixture.nativeElement.remove();
  });
});
