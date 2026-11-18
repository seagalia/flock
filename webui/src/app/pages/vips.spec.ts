import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { provideRouter } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { Api } from '../core/api';
import { Session } from '../core/session';
import { Access } from '../edit/access';
import { Vips } from './vips';

const stored = create(VIPSchema, {
  UUID: 'v1',
  name: 'web-vip',
  ip: '10.0.0.1',
  metadata: { 'flock.autoprovision': 'true' },
});

let list: ReturnType<typeof vi.fn>;

function setup() {
  list = vi.fn(async (kind: string) => (kind === 'vip' ? [stored] : []));
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      { provide: Api, useValue: { list } },
      {
        provide: Access,
        useValue: { check: async () => null, features: async () => ({ ipamEnabled: true }) },
      },
      {
        provide: Session,
        useValue: { user: () => ({ username: 'alice', groups: [], isAdmin: false }) },
      },
      { provide: MatDialog, useValue: { open: vi.fn() } },
    ],
  });
}

async function settle(fixture: { whenStable(): Promise<unknown> }) {
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
}

describe('Vips', () => {
  beforeEach(setup);

  afterEach(() => {
    document
      .querySelectorAll('flock-side-panel, .cdk-overlay-container')
      .forEach((e) => e.remove());
  });

  it('opens a VIP from Edit of its ⋮ menu, with the lock badge on the row', async () => {
    const fixture = TestBed.createComponent(Vips);
    await settle(fixture);
    const page: HTMLElement = fixture.nativeElement;

    const nameCell = page.querySelector<HTMLElement>('td.mat-column-name')!;
    expect(nameCell.querySelector('flock-lock-badge mat-icon')).not.toBeNull();
    page.querySelector<HTMLElement>('flock-resource-menu button')!.click();
    await settle(fixture);
    const items = Array.from(document.body.querySelectorAll<HTMLElement>('[mat-menu-item]'));
    items.find((item) => item.textContent!.includes('Edit'))!.click();
    await settle(fixture);

    const panel = document.body.querySelector('flock-side-panel')!;
    expect(panel.textContent).toContain('web-vip');
    expect(panel.querySelector('.lock')).not.toBeNull();
  });

  it('offers a Create to a non-admin when the ipam is on', async () => {
    const fixture = TestBed.createComponent(Vips);
    await settle(fixture);

    const createButton = fixture.nativeElement.querySelector('button.create') as HTMLButtonElement;
    expect(createButton.getAttribute('aria-disabled')).not.toBe('true');
    createButton.click();
    await settle(fixture);

    const panel = document.body.querySelector('flock-side-panel')!;
    expect(panel.querySelector('[data-card="IP from the ipam"]')).not.toBeNull();
  });
});
