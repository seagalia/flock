import { DatePipe } from '@angular/common';
import { Component, computed, effect, inject, signal, untracked, viewChild } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatDividerModule } from '@angular/material/divider';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { Backend } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { AnnouncerChips, announcerChips, someAgentUnreachable } from '../core/agents';
import { Api, ListPage } from '../core/api';
import { vsBackendUuids, vsType } from '../core/display';
import { InstanceHealth } from '../core/instance-health';
import { Instances } from '../core/instances';
import { kindOf, kinds, linkedToFilter, matchesSearch, Resource } from '../core/resources';
import { vipAddress } from '../core/test-commands';
import { Wizard } from '../core/wizard';
import { Access } from '../edit/access';
import { chainSteps, deleteChain, planChainDeletion } from '../edit/delete-chain';
import { VipAttach } from '../edit/vip-attach';
import { isLocked, lockReason } from '../edit/lock';
import { ResourceEditor } from '../edit/resource-editor';
import { AgentBand } from '../ui/agent-band';
import { AgentPanel } from '../ui/agent-panel';
import { BackendStats } from '../ui/backend-stats';
import { ConfigMap } from '../ui/config-map';
import { confirm } from '../ui/confirm-dialog';
import { CopyButton } from '../ui/copy-button';
import { EndpointSummary } from '../ui/endpoint-summary';
import { ErrorDialog } from '../ui/error-dialog';
import { MenuItem } from '../ui/resource-menu';
import { Column, ExpandedRow, ResourceTable } from '../ui/resource-table';
import { SidePanel } from '../ui/side-panel';
import { TestCommandsMenu } from '../ui/test-commands-menu';

// Overview shows the virtual servers announced by an instance, with their ips and the state of their endpoints
@Component({
  selector: 'flock-overview',
  imports: [
    DatePipe,
    MatTableModule,
    MatDividerModule,
    MatFormFieldModule,
    MatSelectModule,
    MatSlideToggleModule,
    MatButtonModule,
    MatIconModule,
    MatMenuModule,
    MatTooltipModule,
    RouterLink,
    ResourceTable,
    ExpandedRow,
    AgentBand,
    AgentPanel,
    EndpointSummary,
    BackendStats,
    CopyButton,
    TestCommandsMenu,
    SidePanel,
    ConfigMap,
    ResourceEditor,
  ],
  template: `
    <h1>Flock Overview</h1>
    <mat-divider />
    @if (instances.current(); as instance) {
      <flock-resource-table
        [columns]="columns"
        [load]="load()"
        [pageSizes]="[5, 10, 25]"
        [extraItems]="menuItems"
        [showMetadata]="false"
        data-tour="overview"
      >
        <mat-form-field tableControls subscriptSizing="dynamic" data-tour="instance">
          <mat-label>Select an Instance</mat-label>
          <mat-select [value]="instance" (selectionChange)="instances.choose($event.value)">
            @for (one of instances.all(); track one.UUID) {
              <mat-option [value]="one">{{ one.name }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        <mat-slide-toggle
          tableControls
          [checked]="problemsOnly()"
          (change)="showProblemsOnly($event.checked)"
        >
          Problems only
        </mat-slide-toggle>
        <div tableControlsEnd class="state">
          @if (health.updatedAt(); as updatedAt) {
            <span class="updated">updated {{ updatedAt | date: 'HH:mm:ss' }}</span>
          }
          <button mat-icon-button aria-label="Refresh" matTooltip="Refresh" (click)="refresh()">
            <mat-icon>refresh</mat-icon>
          </button>
          <button mat-flat-button type="button" class="expose" (click)="wizard.isOpen.set(true)">
            <mat-icon>add</mat-icon>
            Expose a service
          </button>
        </div>

        <flock-agent-band
          tableBanner
          [instance]="instance"
          [report]="health.agentsReport()"
          [loading]="health.agentsStatus() === null"
          [activeUuid]="openedAgentUuid()"
          (picked)="openedAgentUuid.set($event.agent.UUID)"
        />

        <ng-container matColumnDef="uuid">
          <th mat-header-cell *matHeaderCellDef>UUID</th>
          <td mat-cell *matCellDef="let vs">{{ vs.UUID }}</td>
        </ng-container>
        <ng-container matColumnDef="name">
          <th mat-header-cell *matHeaderCellDef>Name</th>
          <td mat-cell *matCellDef="let vs">
            <a
              [routerLink]="vsLink"
              [queryParams]="{ uuid: vs.UUID }"
              (click)="$event.stopPropagation()"
              >{{ vs.name }}</a
            >
          </td>
        </ng-container>
        <ng-container matColumnDef="typeVs">
          <th mat-header-cell *matHeaderCellDef>Type</th>
          <td mat-cell *matCellDef="let vs">{{ type(vs) }}</td>
        </ng-container>
        <ng-container matColumnDef="ips">
          <th mat-header-cell *matHeaderCellDef>IPs</th>
          <td mat-cell *matCellDef="let vs">
            @for (vip of health.vipsOf(vs); track vip.UUID) {
              <div class="address">
                <a
                  [routerLink]="vipLink"
                  [queryParams]="{ uuid: vip.UUID }"
                  (click)="$event.stopPropagation()"
                  >{{ vip.ip }}</a
                >
                <flock-copy-button [text]="address(vip, vs)" />
                <flock-test-commands-menu [vip]="vip" [vs]="vs" />
                @let chips = announcers(vip);
                @for (announcer of chips.shown; track announcer.label) {
                  <button
                    type="button"
                    class="announcer"
                    [matTooltip]="'Announced by ' + announcer.label + ', open the agent'"
                    (click)="
                      openedAgentUuid.set(announcer.view.agent.UUID); $event.stopPropagation()
                    "
                  >
                    <span [class]="'state-dot ' + announcer.level"></span>
                    {{ announcer.label }}
                  </button>
                }
                @if (chips.others.length > 0) {
                  <button
                    type="button"
                    class="announcer"
                    [matMenuTriggerFor]="othersMenu"
                    [matTooltip]="othersTooltip(chips)"
                    (click)="$event.stopPropagation()"
                  >
                    <span [class]="'state-dot ' + chips.othersLevel"></span>
                    {{
                      chips.allAgents > 0
                        ? 'all ' + chips.allAgents + ' agents'
                        : '+' + chips.others.length
                    }}
                  </button>
                  <mat-menu #othersMenu="matMenu">
                    @for (announcer of chips.others; track announcer.label) {
                      <button
                        mat-menu-item
                        (click)="openedAgentUuid.set(announcer.view.agent.UUID)"
                      >
                        <span [class]="'state-dot ' + announcer.level"></span>
                        {{ announcer.label }}
                      </button>
                    }
                  </mat-menu>
                }
                @if (chips.shown.length === 0 && chips.others.length === 0) {
                  @if (announcerUnknown()) {
                    <span
                      class="announcer unknown"
                      matTooltip="An agent of the instance can't be read, it may be the one announcing this VIP"
                      >announcer unknown</span
                    >
                  }
                }
              </div>
            }
          </td>
        </ng-container>
        <ng-container matColumnDef="port">
          <th mat-header-cell *matHeaderCellDef>Port</th>
          <td mat-cell *matCellDef="let vs">{{ vs.port }}</td>
        </ng-container>
        <ng-container matColumnDef="state">
          <th mat-header-cell *matHeaderCellDef>State</th>
          <td mat-cell *matCellDef="let vs">
            <flock-endpoint-summary [vs]="vs" />
          </td>
        </ng-container>
        <ng-container matColumnDef="map">
          <th mat-header-cell *matHeaderCellDef></th>
          <td mat-cell *matCellDef="let vs">
            <button
              mat-icon-button
              aria-label="Config map"
              matTooltip="Config map"
              data-tour="config-map"
              (click)="mapOf.set(vs); $event.stopPropagation()"
            >
              <mat-icon>account_tree</mat-icon>
            </button>
          </td>
        </ng-container>
        <ng-template flockExpandedRow let-vs>
          <div class="backends">
            @for (backendUuid of backendUuids(vs); track backendUuid) {
              <flock-backend-stats
                [class.half]="backendUuids(vs).length > 1"
                [backendUuid]="backendUuid"
                [endpointStats]="health.endpointStats()?.get(backendUuid) ?? null"
              />
            } @empty {
              <p>This virtual server has no backend.</p>
            }
          </div>
        </ng-template>
      </flock-resource-table>
      @if (openedAgent(); as view) {
        <flock-agent-panel
          [view]="view"
          [instance]="instance"
          (closed)="openedAgentUuid.set(null)"
        />
      }
      @if (mapOf(); as vs) {
        <flock-side-panel
          title="Config map"
          [wide]="true"
          [subject]="vs.name + ' :' + vs.port"
          (closed)="mapOf.set(null)"
        >
          <!-- deferred so ngx-vflow and elkjs stay out of the initial bundle -->
          @defer (on immediate) {
            <flock-config-map [vsUuid]="vs.UUID" [actions]="mapActions" />
          } @loading {
            <p>Loading ...</p>
          }
        </flock-side-panel>
      }
      <!-- the panels the Edit and Delete of the config map open, the overview creates nothing itself -->
      <flock-resource-editor
        #vipEditor
        kind="vip"
        [createButton]="false"
        (changed)="mapChanged()"
      />
      <flock-resource-editor #vsEditor kind="vs" [createButton]="false" (changed)="mapChanged()" />
      <flock-resource-editor
        #backendEditor
        kind="backend"
        [createButton]="false"
        (changed)="mapChanged()"
      />
    } @else if (loaded()) {
      <p class="empty">No instance found, an instance is created when a VIP needs agents.</p>
    }
  `,
  styles: `
    .state {
      display: flex;
      align-items: center;
      gap: 8px;
    }
    .updated {
      color: var(--mat-sys-on-surface-variant);
      font-size: 13px;
    }
    .announcer {
      display: inline-flex;
      align-items: center;
      gap: 5px;
      margin-left: 6px;
      padding: 0 8px;
      font: inherit;
      font-size: 12px;
      white-space: nowrap;
      color: var(--mat-sys-on-surface-variant);
      background: transparent;
      border: 1px solid var(--flock-border);
      border-radius: 999px;
      cursor: pointer;
      &:hover {
        background: var(--flock-hover);
        color: var(--mat-sys-on-surface);
      }
      &.unknown {
        cursor: help;
      }
      .state-dot {
        width: 8px;
        height: 8px;
      }
    }
    .address {
      display: flex;
      align-items: center;
      gap: 2px;
      // the ips differ in width: the buttons go to the end of the cell to line up from row to row
      a {
        margin-right: auto;
        padding-right: 8px;
      }
    }
    .backends {
      display: flex;
      flex-wrap: wrap;
      width: 100%;
      > * {
        flex-basis: 100%;
        min-width: 0;
      }
      > .half {
        flex-basis: 50%;
      }
    }
    @media (max-width: 900px) {
      .backends > .half {
        flex-basis: 100%;
      }
    }
    .empty {
      margin: 20px 0;
    }
  `,
})
export class Overview {
  private api = inject(Api);
  private access = inject(Access);
  private dialog = inject(MatDialog);
  private vipAttach = inject(VipAttach);
  private router = inject(Router);
  protected instances = inject(Instances);
  protected health = inject(InstanceHealth);
  protected wizard = inject(Wizard);
  protected loaded = signal(false);
  protected problemsOnly = signal(false);
  protected mapOf = signal<VirtualServer | null>(null);
  // the panel follows the agent through the refreshes, and closes when another instance is chosen
  protected openedAgentUuid = signal<string | null>(null);
  protected openedAgent = computed(() =>
    this.health.agentsReport().agents.find((view) => view.agent.UUID === this.openedAgentUuid()),
  );
  // a VIP announced by none of the agents read may be held by the one that could not be read
  protected announcerUnknown = computed(() => someAgentUnreachable(this.health.agentsReport()));
  private vipEditor = viewChild<ResourceEditor>('vipEditor');
  private vsEditor = viewChild<ResourceEditor>('vsEditor');
  private backendEditor = viewChild<ResourceEditor>('backendEditor');
  private configMap = viewChild(ConfigMap);
  // the refresh button lists the virtual servers again, the timer only refreshes their health
  private reloads = signal(0);
  // a change of the vips of the instance lists again, a refresh giving the same ones does not
  private vipKey = computed(() =>
    this.health
      .vips()
      .map((vip) => vip.UUID)
      .join(','),
  );

  protected readonly vsLink = kinds.vs.link;
  protected readonly vipLink = kinds.vip.link;
  protected readonly columns: Column[] = [
    { key: 'uuid', label: 'UUID' },
    { key: 'name', label: 'Name' },
    { key: 'typeVs', label: 'Type' },
    { key: 'ips', label: 'IPs' },
    { key: 'port', label: 'Port' },
    { key: 'state', label: 'State' },
    { key: 'map', label: 'Map' },
  ];

  constructor() {
    const params = inject(ActivatedRoute).snapshot.queryParamMap;
    this.problemsOnly.set(params.get('problems') === '1');
    this.instances.load(params.get('instance')).finally(() => this.loaded.set(true));
    // the wizard may have created a virtual server on this instance, the table lists again once it closes, as the
    // other pages do after a Create
    let wasOpen = false;
    effect(() => {
      const isOpen = this.wizard.isOpen();
      const closed = wasOpen && !isOpen;
      wasOpen = isOpen;
      if (closed) {
        untracked(() => this.refresh());
      }
    });
  }

  // load gives a new function on each change, which makes the table list again from its first page
  protected load = computed(() => {
    this.reloads();
    this.vipKey();
    const problemsOnly = this.problemsOnly();
    return untracked(() =>
      problemsOnly
        ? (page: ListPage) => this.listProblems(page)
        : (page: ListPage) => this.listAll(page),
    );
  });

  protected type = (vs: VirtualServer) => vsType(vs);
  protected backendUuids = (vs: VirtualServer) => vsBackendUuids(vs);
  protected address = (vip: VIP, vs: VirtualServer) => vipAddress(vip, vs);
  protected announcers = (vip: VIP) => announcerChips(this.health.agentsReport(), vip.UUID);

  protected othersTooltip(chips: AnnouncerChips): string {
    const names = chips.others.map((one) => one.label).join(', ');
    if (chips.allAgents > 0) {
      return `Announced by every agent of the instance: ${names}. Open an agent`;
    }
    return `Also announced by ${names}. Open an agent`;
  }

  protected showProblemsOnly(problemsOnly: boolean) {
    this.problemsOnly.set(problemsOnly);
    this.router.navigate([], {
      queryParams: { problems: problemsOnly ? '1' : null },
      queryParamsHandling: 'merge',
      replaceUrl: true,
    });
  }

  protected menuItems = (row: Resource): MenuItem[] => [
    { label: 'Attach to VIPs', icon: 'link', run: () => this.editVips(row as VirtualServer) },
    this.chainDeleteItem(row as VirtualServer),
  ];

  private chainDeleteItem(vs: VirtualServer): MenuItem {
    return {
      label: 'Delete',
      icon: 'delete',
      tone: 'danger',
      disabled: isLocked(vs) ? lockReason : undefined,
      run: () => this.removeChain(vs),
    };
  }

  private async editVips(vs: VirtualServer) {
    const changed = await this.vipAttach.edit(vs);
    if (changed) {
      await this.refresh();
    }
  }

  // removeChain deletes the virtual server with the backends only it uses, the chain its config map shows. Its VIPs
  // are kept and only stop listing it, another virtual server may share them.
  private async removeChain(vs: VirtualServer) {
    const refuse = (title: string, message: string) =>
      this.dialog.open(ErrorDialog, { data: { title, message } });
    // the error dialog of the api already tells why a read failed
    const plan = await planChainDeletion(this.api, this.access, vs).catch(() => null);
    if (!plan) {
      return;
    }
    if (plan.refusal) {
      refuse(`Can't delete the virtual server ${vs.name}`, plan.refusal);
      return;
    }
    const kept = plan.kept.map((backend) => `keep the backend ${backend.name}, ${backend.reason}`);
    const agreed = await confirm(this.dialog, {
      title: `Delete the virtual server ${vs.name} and its chain?`,
      message: 'In this order:',
      items: [...chainSteps(plan), ...kept],
      confirm: 'Delete',
    });
    if (!agreed) {
      return;
    }
    const outcome = await deleteChain(this.api, plan);
    if (outcome.error) {
      const done = outcome.done.length > 0 ? ` Done: ${outcome.done.join(', ')}.` : '';
      refuse(
        `Deleting the chain of ${vs.name} stopped`,
        `${outcome.error}. Not done: ${outcome.notDone.join(', ')}.${done}`,
      );
    }
    await this.refresh();
  }

  // mapActions are the Edit and Delete of a node of the config map. The virtual server is deleted with its chain, as
  // from its row, since a VIP listing it would refuse its plain deletion.
  protected mapActions = (resource: VIP | VirtualServer | Backend): MenuItem[] => {
    const editors = { vip: this.vipEditor(), vs: this.vsEditor(), backend: this.backendEditor() };
    const kind = kindOf(resource).name as keyof typeof editors;
    const items = editors[kind]?.menuItems(resource) ?? [];
    const edit = items.filter((item) => item.label === 'Edit');
    if (kind === 'vs') {
      return [...edit, this.chainDeleteItem(resource as VirtualServer)];
    }
    return items.filter((item) => item.tone !== undefined);
  };

  // mapChanged follows an edit or a deletion made from the config map, the map reads its backends only on a change of
  // their list
  protected async mapChanged() {
    await this.refresh();
    this.configMap()?.reloadBackends();
  }

  protected async refresh() {
    await this.health.refresh();
    this.reloads.update((count) => count + 1);
    // the map of a virtual server deleted meanwhile, from its row or its own map, has nothing left to draw
    const mapped = this.mapOf();
    const isMappedGone =
      mapped !== null && !this.health.vss().some((vs) => vs.UUID === mapped.UUID);
    if (isMappedGone) {
      this.mapOf.set(null);
    }
  }

  private async listAll(page: ListPage): Promise<Resource[]> {
    const vips = this.health.vips();
    // with no filter the list would give every virtual server, not the ones of this instance
    if (vips.length === 0) {
      return [];
    }
    const vipFilters = vips.map((vip) => linkedToFilter(vip));
    return this.api.list('vs', { ...page, filters: [...vipFilters, ...page.filters] });
  }

  // listProblems pages the degraded virtual servers in the browser: the api cannot filter on health
  private async listProblems(page: ListPage): Promise<Resource[]> {
    const matching = this.health.degraded().filter((vs) => matchesSearch(vs, page.filters));
    return matching.slice(page.offset, page.offset + page.limit);
  }
}
