import {
  Component,
  computed,
  DestroyRef,
  effect,
  inject,
  input,
  signal,
  untracked,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { timestampDate } from '@bufbuild/protobuf/wkt';
import { MatButtonModule } from '@angular/material/button';
import { MatDividerModule } from '@angular/material/divider';
import { MatIconModule } from '@angular/material/icon';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { Agent } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { Instance } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { GetAgentStatusResponse, InstanceNode } from '../../gen/flock/services/lookup/v1/lookup_pb';
import { agentsReport, AgentView, isVersionDrift, levelLabels, timeAgo } from '../core/agents';
import { Api, silentCall } from '../core/api';
import { kinds } from '../core/resources';
import { AgentPanel } from './agent-panel';

const shownVips = 3;

// AgentsTable lists the agents of an instance with what they report (state, version, last connection, VIPs), its
// provisioners, the nodes connected with no declared agent, and opens an agent in a side panel on click. The query
// parameter agent opens the panel of that agent, so a link can point at one.
@Component({
  selector: 'flock-agents-table',
  imports: [
    MatTableModule,
    MatDividerModule,
    MatButtonModule,
    MatIconModule,
    MatTooltipModule,
    RouterLink,
    AgentPanel,
  ],
  template: `
    <div class="head">
      <h2>Agents</h2>
      @if (agents().length > 0) {
        <span class="meta">
          @if (report().agents.length > 0) {
            {{ healthy() }} / {{ report().agents.length }} healthy
          }
          @if (loadedAt(); as loadedAt) {
            · read at {{ loadedAt.toLocaleTimeString() }}
          }
          <button mat-icon-button aria-label="Refresh agents" matTooltip="Refresh" (click)="load()">
            <mat-icon>refresh</mat-icon>
          </button>
        </span>
      }
    </div>
    <mat-divider />
    @if (report().agents.length > 0) {
      <div class="bordered">
        <table mat-table [dataSource]="report().agents">
          <ng-container matColumnDef="state">
            <th mat-header-cell *matHeaderCellDef aria-label="State"></th>
            <td mat-cell *matCellDef="let view">
              <span [class]="'state-dot ' + view.level" [matTooltip]="levelLabel(view)"></span>
            </td>
          </ng-container>
          <ng-container matColumnDef="name">
            <th mat-header-cell *matHeaderCellDef>Name</th>
            <td mat-cell *matCellDef="let view">
              <span class="name">{{ view.agent.name }}</span>
              @if (view.cause) {
                <span [class]="'cause ' + view.level">{{ view.cause }}</span>
              }
              @if (view.isBridge && view.bridgeComponents.length > 0) {
                <span class="components">
                  @for (component of view.bridgeComponents; track component.name) {
                    <span>
                      <span [class]="'state-dot ' + component.level"></span>
                      {{ component.name }} · {{ component.vips.length }} VIPs
                    </span>
                  }
                </span>
              }
            </td>
          </ng-container>
          <ng-container matColumnDef="host">
            <th mat-header-cell *matHeaderCellDef>Host</th>
            <td mat-cell *matCellDef="let view">{{ view.agent.host }}</td>
          </ng-container>
          <ng-container matColumnDef="mode">
            <th mat-header-cell *matHeaderCellDef>Mode</th>
            <td mat-cell *matCellDef="let view">
              <span class="mode">{{ view.isBridge ? 'bridge' : 'sidecar' }}</span>
            </td>
          </ng-container>
          <ng-container matColumnDef="version">
            <th mat-header-cell *matHeaderCellDef>Version</th>
            <td mat-cell *matCellDef="let view">
              @if (isDrift(view)) {
                <span
                  class="drift"
                  [matTooltip]="
                    'Differs from ' + report().commonVersion + ', the version most agents run'
                  "
                  >{{ view.version }} ⚠</span
                >
              } @else {
                {{ view.version || '–' }}
              }
            </td>
          </ng-container>
          <ng-container matColumnDef="connected">
            <th mat-header-cell *matHeaderCellDef>Connected</th>
            <td mat-cell *matCellDef="let view">
              @if (view.lastSeen) {
                <span [matTooltip]="view.lastSeen.toLocaleString()">{{ ago(view.lastSeen) }}</span>
              } @else {
                <span class="muted">never</span>
              }
            </td>
          </ng-container>
          <ng-container matColumnDef="vips">
            <th mat-header-cell *matHeaderCellDef>VIPs announced</th>
            <td mat-cell *matCellDef="let view">
              @if (view.isBridge) {
                <span class="muted">per component</span>
              } @else if (view.vips; as vips) {
                <span class="vips">
                  @for (vip of vips.slice(0, shownVips); track vip.UUID) {
                    <a
                      [routerLink]="vipLink"
                      [queryParams]="{ uuid: vip.UUID }"
                      (click)="$event.stopPropagation()"
                      >{{ vip.name }}</a
                    >
                  } @empty {
                    <span class="muted">–</span>
                  }
                  @if (vips.length > shownVips) {
                    <span class="muted" [matTooltip]="moreVips(vips)"
                      >+{{ vips.length - shownVips }}</span
                    >
                  }
                </span>
              } @else {
                <span class="muted">?</span>
              }
            </td>
          </ng-container>
          <tr mat-header-row *matHeaderRowDef="columns"></tr>
          <tr
            mat-row
            *matRowDef="let view; columns: columns"
            class="agent"
            [class.active]="view.agent.UUID === openedUuid()"
            tabindex="0"
            (click)="openedUuid.set(view.agent.UUID)"
            (keydown.enter)="openedUuid.set(view.agent.UUID)"
          ></tr>
        </table>
      </div>
    } @else if (loaded()) {
      <p class="muted empty">
        This instance has no agent yet. Agents join it from the instantiator config of its zone.
      </p>
    }
    @if (report().provisioners.length > 0) {
      <div class="provisioners">
        <h3>Provisioners</h3>
        @for (view of report().provisioners; track view.agent.UUID) {
          <div
            class="line provisioner"
            [class.active]="view.agent.UUID === openedUuid()"
            tabindex="0"
            (click)="openedUuid.set(view.agent.UUID)"
            (keydown.enter)="openedUuid.set(view.agent.UUID)"
          >
            <span [class]="'state-dot ' + view.level" [matTooltip]="levelLabel(view)"></span>
            <span class="name">{{ view.agent.name }}</span>
            <span class="muted">{{ view.agent.host }}</span>
            <span class="muted">{{ view.version || '–' }}</span>
            @if (view.lastSeen) {
              <span class="muted" [matTooltip]="view.lastSeen.toLocaleString()"
                >connected {{ ago(view.lastSeen) }}</span
              >
            } @else {
              <span class="muted">never connected</span>
            }
            @if (view.cause) {
              <span [class]="'cause ' + view.level">{{ view.cause }}</span>
            }
          </div>
        }
      </div>
    }
    @if (report().undeclared.length > 0) {
      <div class="undeclared">
        <h3>Connected, not declared</h3>
        @for (node of report().undeclared; track node.name) {
          <div class="line">
            <span class="state-dot"></span>
            <span class="name">{{ node.name }}</span>
            <span class="muted">{{ undeclaredDetails(node) }}</span>
          </div>
        }
      </div>
    }
    @if (opened(); as view) {
      <flock-agent-panel [view]="view" [instance]="instance()" (closed)="closePanel()" />
    }
  `,
  styles: `
    .head {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
    }
    .meta {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      color: var(--mat-sys-on-surface-variant);
      font-size: 13px;
    }
    .bordered {
      border: 1px solid var(--flock-border);
      border-radius: 8px;
      overflow: auto;
      margin: 10px 0 20px;
    }
    tr.agent {
      cursor: pointer;
      &:hover {
        background: var(--flock-hover);
      }
      &.active {
        background: var(--flock-selected);
      }
    }
    .name {
      font-weight: 600;
    }
    .cause {
      display: block;
      font-size: 13px;
      &.warning {
        color: var(--flock-warning-text);
      }
      &.error {
        color: var(--flock-error-text);
      }
    }
    .components {
      display: flex;
      flex-wrap: wrap;
      gap: 2px 14px;
      font-size: 13px;
      > span {
        display: inline-flex;
        align-items: center;
        gap: 5px;
      }
    }
    .mode {
      font-size: 12px;
      border: 1px solid var(--flock-border);
      border-radius: 999px;
      padding: 1px 8px;
    }
    .drift {
      color: var(--flock-warning-text);
      font-weight: 600;
    }
    .vips {
      display: flex;
      flex-wrap: wrap;
      gap: 2px 10px;
    }
    .muted {
      color: var(--mat-sys-on-surface-variant);
    }
    .empty {
      margin: 10px 0 20px;
    }
    .provisioners {
      border: 1px solid var(--flock-border);
      border-radius: 8px;
      padding: 10px 14px;
      margin: 0 0 20px;
      .provisioner {
        cursor: pointer;
        border-radius: 6px;
        padding: 2px 4px;
        &:hover {
          background: var(--flock-hover);
        }
        &.active {
          background: var(--flock-selected);
        }
      }
    }
    .provisioners h3,
    .undeclared h3 {
      margin: 0 0 4px;
      font-size: 13px;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.06em;
      color: var(--mat-sys-on-surface-variant);
    }
    .provisioners .line,
    .undeclared .line {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 4px 12px;
    }
    .undeclared {
      border: 1px dashed var(--flock-warning);
      border-radius: 8px;
      padding: 10px 14px;
      margin: 0 0 20px;
    }
  `,
})
export class AgentsTable {
  readonly instance = input.required<Instance>();
  private api = inject(Api);
  private router = inject(Router);
  private route = inject(ActivatedRoute);

  protected readonly columns = ['state', 'name', 'host', 'mode', 'version', 'connected', 'vips'];
  protected readonly shownVips = shownVips;
  protected readonly vipLink = kinds.vip.link;
  protected readonly ago = (date: Date) => timeAgo(date);

  protected agents = signal<Agent[]>([]);
  private statuses = signal<GetAgentStatusResponse[] | null>(null);
  private nodes = signal<InstanceNode[]>([]);
  protected loaded = signal(false);
  protected loadedAt = signal<Date | null>(null);
  // the panel follows the agent through the refreshes
  protected openedUuid = signal<string | null>(null);

  protected report = computed(() => agentsReport(this.agents(), this.statuses(), this.nodes()));
  protected opened = computed(() => {
    const report = this.report();
    const views = [...report.agents, ...report.provisioners];
    return views.find((view) => view.agent.UUID === this.openedUuid());
  });
  protected healthy = computed(
    () => this.report().agents.filter((view) => view.level === 'ok').length,
  );

  constructor() {
    effect(() => {
      this.instance();
      untracked(() => this.load());
    });
    // an agent of another instance matches no view of this one, its panel stays closed
    this.route.queryParamMap.pipe(takeUntilDestroyed(inject(DestroyRef))).subscribe((params) => {
      const agentUuid = params.get('agent');
      if (agentUuid) {
        this.openedUuid.set(agentUuid);
      }
    });
  }

  // the agent leaves the url too, so that expanding the row again doesn't reopen its panel
  protected closePanel() {
    this.openedUuid.set(null);
    if (this.route.snapshot.queryParamMap.has('agent')) {
      this.router.navigate([], {
        queryParams: { agent: null },
        queryParamsHandling: 'merge',
        replaceUrl: true,
      });
    }
  }

  // load reads the agents once per expand and on refresh: the status call reaches every agent, and the row is a
  // listing rather than a monitoring view. Each call fails quietly, an unreachable agent is shown as such.
  protected async load() {
    const instanceUuid = this.instance().UUID;
    const [agents, statuses, nodes] = await Promise.allSettled([
      this.api.lookup.listAgent({ instanceUuid }, silentCall),
      this.api.lookup.listAgentStatus({ instanceUuid, showAnnouncingVips: true }, silentCall),
      this.api.lookup.listInstanceNodes({ instanceUuid }, silentCall),
    ]);
    this.agents.set(agents.status === 'fulfilled' ? agents.value.agents : []);
    this.statuses.set(statuses.status === 'fulfilled' ? statuses.value.agentStatus : []);
    this.nodes.set(nodes.status === 'fulfilled' ? nodes.value.nodes : []);
    this.loaded.set(true);
    this.loadedAt.set(new Date());
  }

  protected levelLabel(view: AgentView): string {
    return levelLabels[view.level];
  }

  protected isDrift(view: AgentView): boolean {
    return isVersionDrift(this.report(), view);
  }

  protected moreVips(vips: VIP[]): string {
    return vips
      .slice(shownVips)
      .map((vip) => vip.name)
      .join(', ');
  }

  protected undeclaredDetails(node: InstanceNode): string {
    const seen = node.lastSeen ? `node seen ${timeAgo(timestampDate(node.lastSeen))}` : '';
    const why = 'no agent of the instantiator config has its uuid';
    return [seen, why].filter(Boolean).join(' · ');
  }
}
