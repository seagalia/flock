import { signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { CliHost } from '../core/cli-host';
import { InstanceHealth } from '../core/instance-health';
import { Instances } from '../core/instances';
import { Session, SessionUser } from '../core/session';
import { Theme } from '../core/theme';
import { Shell } from './shell';

describe('Shell mode banner', () => {
  const user = signal<SessionUser | null>(null);

  async function render(cli: { cwd: string } | null, standalone: { controller: string } | null) {
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        {
          provide: CliHost,
          useValue: { info: async () => cli, standaloneInfo: async () => standalone },
        },
        { provide: Instances, useValue: { load: async () => [], current: signal(null) } },
        { provide: InstanceHealth, useValue: { degraded: () => [], degradedLevel: () => 'ok' } },
        { provide: Theme, useValue: { isDark: () => false, toggle: () => undefined } },
        { provide: Session, useValue: { user, kind: () => 'cli' } },
      ],
    });
    const fixture = TestBed.createComponent(Shell);
    await fixture.whenStable();
    fixture.detectChanges();
    return fixture.nativeElement.querySelector('.mode-banner') as HTMLElement | null;
  }

  beforeEach(() => {
    // jsdom has no matchMedia, which the breakpoint observer of the shell reads
    vi.stubGlobal('matchMedia', () => ({
      matches: false,
      media: '',
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
    }));
    user.set({ username: 'alice', groups: [], isAdmin: false });
  });

  afterEach(() => vi.unstubAllGlobals());

  it('names the user of the cli session in local mode', async () => {
    const banner = await render({ cwd: '/work' }, null);

    expect(banner?.textContent?.trim()).toBe(
      'Local mode, acting as alice through your cli session',
    );
  });

  it('names the controller in standalone mode', async () => {
    const banner = await render(null, { controller: 'flock.example.com' });

    expect(banner?.textContent?.trim()).toBe('Standalone dashboard of flock.example.com');
  });

  it('shows nothing on a controller', async () => {
    expect(await render(null, null)).toBeNull();
  });

  it('leaves the user out of the local banner when the cli session has none', async () => {
    user.set(null);
    const banner = await render({ cwd: '/work' }, null);

    expect(banner?.textContent?.trim()).toBe('Local mode, acting through your cli session');
  });
});
