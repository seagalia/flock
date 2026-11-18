import { BreakpointObserver } from '@angular/cdk/layout';
import { Component, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatDividerModule } from '@angular/material/divider';
import { MatIconModule } from '@angular/material/icon';
import { MatListModule } from '@angular/material/list';
import { MatMenuModule } from '@angular/material/menu';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSidenavModule } from '@angular/material/sidenav';
import { MatToolbarModule } from '@angular/material/toolbar';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { map } from 'rxjs';
import { Builder } from '../builder/builder';
import { CliHost } from '../core/cli-host';
import { InstanceHealth } from '../core/instance-health';
import { Instances } from '../core/instances';
import { Progress } from '../core/notifier';
import { Oidc } from '../core/oidc';
import { ResourceCreations } from '../core/resource-creations';
import { Session } from '../core/session';
import { Theme } from '../core/theme';
import { Tour } from '../core/tour';
import { Wizard } from '../core/wizard';
import { ResourceEditor } from '../edit/resource-editor';
import { SidePanel } from '../ui/side-panel';

interface NavItem {
  label: string;
  icon: string;
  link: string;
}

@Component({
  selector: 'flock-shell',
  imports: [
    RouterOutlet,
    RouterLink,
    RouterLinkActive,
    MatSidenavModule,
    MatToolbarModule,
    MatListModule,
    MatIconModule,
    MatButtonModule,
    MatMenuModule,
    MatDividerModule,
    MatProgressBarModule,
    MatTooltipModule,
    SidePanel,
    Builder,
    ResourceEditor,
  ],
  templateUrl: './shell.html',
  styleUrl: './shell.scss',
})
export class Shell {
  protected progress = inject(Progress);
  protected session = inject(Session);
  protected theme = inject(Theme);
  // closing the panel removes the wizard, the next opening starts a new run
  protected wizard = inject(Wizard);
  protected creations = inject(ResourceCreations);
  protected tour = inject(Tour);
  // the health of the selected instance is followed from every page, for the badge of the overview
  protected health = inject(InstanceHealth);
  protected isHandset = toSignal(
    inject(BreakpointObserver)
      .observe('(max-width: 900px)')
      .pipe(map((state) => state.matches)),
    { initialValue: false },
  );
  protected lockup = computed(() =>
    this.theme.isDark() ? 'brand/lockup-dark.svg' : 'brand/lockup.svg',
  );
  protected symbol = computed(() =>
    this.theme.isDark() ? 'brand/symbol-dark.svg' : 'brand/symbol.svg',
  );
  protected readonly nav: NavItem[] = [
    { label: 'Overview', icon: 'dashboard', link: '/' },
    { label: 'Virtual Servers', icon: 'dns', link: '/virtualservers' },
    { label: 'Backends', icon: 'hub', link: '/backends' },
    { label: 'VIPs', icon: 'settings_ethernet', link: '/vips' },
    { label: 'Certificates', icon: 'verified_user', link: '/certificates' },
    { label: 'Secrets', icon: 'key', link: '/secrets' },
    { label: 'Instances', icon: 'memory', link: '/instances' },
    { label: 'Zones', icon: 'public', link: '/zones' },
    { label: 'Status', icon: 'monitor_heart', link: '/status' },
    { label: 'Monitors', icon: 'terminal', link: '/monitors' },
  ];
  private instances = inject(Instances);
  protected degradedTooltip = computed(() => {
    const count = this.health.degraded().length;
    const servers = count === 1 ? 'virtual server' : 'virtual servers';
    return `${count} ${servers} with unhealthy endpoints on ${this.instances.current()?.name}`;
  });
  private router = inject(Router);
  private oidc = inject(Oidc);

  private cliHost = inject(CliHost);
  // the mode is read once at start, the page never changes mode while it is open
  private mode = signal<{ cwd?: string; controller?: string } | null>(null);
  protected modeBanner = computed(() => {
    const mode = this.mode();
    if (mode?.controller) {
      return `Standalone dashboard of ${mode.controller}`;
    }
    if (!mode?.cwd) {
      return null;
    }
    // the cli can run on FLOCK_TOKEN, where no user name is known
    const username = this.session.user()?.username;
    return username
      ? `Local mode, acting as ${username} through your cli session`
      : 'Local mode, acting through your cli session';
  });

  constructor() {
    this.instances.load();
    void this.loadMode();
    void this.tour.offerOnce();
  }

  private async loadMode() {
    const [local, standalone] = await Promise.all([
      this.cliHost.info(),
      this.cliHost.standaloneInfo(),
    ]);
    this.mode.set(standalone ?? local);
  }

  // logout also ends the session at the OIDC provider, which then sends the browser back to the login page
  protected async logout() {
    const isOidcSession = this.session.kind() === 'oidc';
    this.session.clear();
    try {
      if (isOidcSession) {
        await this.oidc.signOut();
      }
    } finally {
      // the local session is gone even when the provider can't be reached
      this.router.navigate(['/login']);
    }
  }
}
