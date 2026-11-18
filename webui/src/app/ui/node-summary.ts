import { Component, computed, inject, input } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterLink } from '@angular/router';
import { isMessage } from '@bufbuild/protobuf';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import {
  VirtualServer,
  VirtualServerSchema,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { endpointAddress, GraphNode, NodeKind } from '../core/config-graph';
import {
  backendType,
  HealthLevel,
  lbPolicyLabel,
  routeActionLabel,
  routeMatchLabel,
  vsType,
} from '../core/display';
import { InstanceHealth } from '../core/instance-health';
import { kinds } from '../core/resources';
import { MenuItem } from './resource-menu';
import { TestCommandsMenu } from './test-commands-menu';

const kindLabels: Record<NodeKind, string> = {
  vip: 'VIP',
  vs: 'Virtual Server',
  route: 'Route',
  backend: 'Backend',
  endpoint: 'Endpoint',
};

interface Row {
  label: string;
  value: string;
  // colors the value like the status dot of the node
  level?: HealthLevel | null;
}

// NodeSummary is the card of a node selected on the config map: what the resource is, what is wrong with it,
// and the ways to edit it or reach its page
@Component({
  selector: 'flock-node-summary',
  imports: [MatButtonModule, MatIconModule, MatTooltipModule, RouterLink, TestCommandsMenu],
  template: `
    <div class="summary" role="dialog" [attr.aria-label]="kindLabel() + ' ' + node().title">
      <div class="heading">
        <span class="title">{{ node().title }}</span>
        <span class="kind">{{ kindLabel() }}</span>
      </div>
      @if (rows().length > 0) {
        <div class="rows">
          @for (row of rows(); track row.label) {
            <span class="label">{{ row.label }}</span>
            <span [class]="row.level ?? ''">{{ row.value }}</span>
          }
        </div>
      }
      @for (problem of problems(); track $index) {
        <div class="problem">{{ problem }}</div>
      }
      @if (backends().length > 0) {
        <div class="backends">
          <span class="label">Backends</span>
          @for (backend of backends(); track backend.uuid) {
            <a [routerLink]="backendLink" [queryParams]="{ uuid: backend.uuid }">{{
              backend.name
            }}</a>
          }
        </div>
      }
      @if (test(); as test) {
        <div class="test">
          <span class="label">Test</span>
          @for (vip of test.vips; track vip.UUID) {
            <span class="vip"
              >{{ vip.ip }}
              <flock-test-commands-menu [vip]="vip" [vs]="test.vs" [route]="test.route" />
            </span>
          }
        </div>
      }
      @if (actions().length > 0) {
        <div class="actions">
          @for (item of actions(); track item.label) {
            <button
              mat-stroked-button
              type="button"
              [class.primary]="item.tone === 'primary'"
              [class.danger]="item.tone === 'danger'"
              [disabled]="!!item.disabled"
              disabledInteractive
              [matTooltip]="item.disabled ?? ''"
              (click)="runAction(item)"
            >
              <mat-icon>{{ item.icon }}</mat-icon>
              {{ item.label }}
            </button>
          }
        </div>
      }
      @if (pageLink()) {
        <div class="actions">
          @if (pageLink(); as link) {
            <a mat-flat-button [routerLink]="link.path" [queryParams]="{ uuid: link.uuid }">{{
              link.label
            }}</a>
          }
        </div>
      }
    </div>
  `,
  styles: `
    .summary {
      width: 256px;
      padding: 16px;
      display: flex;
      flex-direction: column;
      gap: 10px;
      background: var(--mat-sys-surface);
      border: 1px solid var(--flock-border);
      border-radius: 12px;
      box-shadow: var(--mat-sys-level3);
      font-size: 13px;
      line-height: 20px;
      cursor: default;
    }
    .heading {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: 8px;
    }
    .title {
      font-size: 16px;
      font-weight: 700;
      overflow-wrap: anywhere;
    }
    .kind {
      flex: none;
      font-size: 11px;
      font-weight: 700;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: var(--mat-sys-on-surface-variant);
    }
    .rows {
      display: grid;
      grid-template-columns: 84px 1fr;
      row-gap: 4px;
      column-gap: 8px;
      overflow-wrap: anywhere;
    }
    .label {
      color: var(--mat-sys-on-surface-variant);
    }
    .ok {
      color: var(--flock-ok-text);
      font-weight: 600;
    }
    .warning {
      color: var(--flock-warning-text);
      font-weight: 600;
    }
    .error {
      color: var(--flock-error-text);
      font-weight: 600;
    }
    .problem {
      padding: 8px 10px;
      border-radius: 8px;
      background: color-mix(in srgb, var(--flock-error) 15%, transparent);
      color: var(--flock-error-text);
      overflow-wrap: anywhere;
    }
    .backends {
      display: flex;
      flex-direction: column;
      font-family: var(--flock-mono);
      font-size: 12px;
      .label {
        font-family: initial;
        font-size: 13px;
      }
    }
    .test {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      column-gap: 12px;
    }
    .vip {
      display: inline-flex;
      align-items: center;
      font-family: var(--flock-mono);
      font-size: 12px;
    }
    .actions {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
    }
    .primary:not([aria-disabled='true']) {
      color: var(--mat-sys-primary);
    }
    .danger:not([aria-disabled='true']) {
      color: var(--flock-error-text);
    }
  `,
})
export class NodeSummary {
  readonly node = input.required<GraphNode>();
  // what the page offers on the resource of the node, Edit and Delete
  readonly actions = input<MenuItem[]>([]);

  private instanceHealth = inject(InstanceHealth);
  protected readonly backendLink = kinds.backend.link;

  // disabledInteractive keeps a greyed button clickable for its tooltip, so the click itself must be ignored
  protected runAction(item: MenuItem) {
    if (item.disabled) {
      return;
    }
    item.run();
  }

  protected kindLabel = computed(() => kindLabels[this.node().kind]);

  protected rows = computed<Row[]>(() => {
    const node = this.node();
    const resource = node.resource;
    if (isMessage(resource, VIPSchema)) {
      return [
        { label: 'IP', value: resource.ip },
        { label: 'Name', value: resource.name },
      ];
    }
    if (node.kind === 'vs' && isMessage(resource, VirtualServerSchema)) {
      return [
        { label: 'Type', value: vsType(resource) },
        { label: 'Port', value: String(resource.port) },
        { label: 'Health', value: this.vsHealthLabel(resource), level: node.health },
      ];
    }
    if (node.kind === 'route') {
      return this.routeRows(node);
    }
    if (isMessage(resource, BackendSchema)) {
      return [
        { label: 'Discovery', value: backendType(resource) },
        { label: 'Balancing', value: lbPolicyLabel(resource) },
        { label: 'Health', value: node.subtitle, level: node.health },
      ];
    }
    if (node.kind === 'endpoint') {
      const reports = node.reports ?? [];
      const down = reports.filter((report) => !report.healthy).length;
      const health =
        down > 0
          ? `down on ${down} of ${reports.length} reports`
          : `up on ${reports.length} of ${reports.length} reports`;
      return [
        { label: 'Address', value: node.title },
        { label: 'Health', value: health, level: node.health },
      ];
    }
    return [];
  });

  // what is wrong: the endpoints of a backend which are down, the errors of an endpoint, a missing backend
  protected problems = computed<string[]>(() => {
    const node = this.node();
    if (node.missing) {
      return [`Backend ${node.subtitle} was not found`];
    }
    if (node.kind === 'endpoint') {
      const errors = (node.reports ?? []).map((report) => report.lastError ?? '');
      return [...new Set(errors.filter((error) => error !== ''))];
    }
    if (!isMessage(node.resource, BackendSchema)) {
      return [];
    }
    const reports = this.instanceHealth.endpointStats()?.get(node.resource.UUID)?.endpointStats;
    return (reports ?? [])
      .filter((report) => !report.healthy)
      .map((report) => {
        const address = endpointAddress(report.ipAddr?.ip ?? '', report.ipAddr?.port ?? 0);
        return report.lastError ? `${address} down: ${report.lastError}` : `${address} down`;
      });
  });

  // an endpoint is in every backend reporting it, it has no page of its own
  protected backends = computed(() => {
    const node = this.node();
    if (node.kind !== 'endpoint') {
      return [];
    }
    const names = new Map(
      (node.reports ?? []).map((report) => [report.backendUuid, report.backendName]),
    );
    return [...names].map(([uuid, name]) => ({ uuid, name }));
  });

  protected test = computed(() => {
    const node = this.node();
    const resource = node.resource;
    const isTestable = node.kind === 'vs' || node.kind === 'route';
    if (!isTestable || !isMessage(resource, VirtualServerSchema)) {
      return null;
    }
    const vips = this.instanceHealth.vipsOf(resource);
    if (vips.length === 0) {
      return null;
    }
    return { vs: resource, vips, route: node.testRoute };
  });

  protected pageLink = computed(() => {
    const node = this.node();
    const uuid = node.resource?.UUID;
    if (!uuid) {
      return null;
    }
    switch (node.kind) {
      case 'vip':
        return { path: kinds.vip.link, label: 'Go to VIPs', uuid };
      case 'vs':
      case 'route':
        return { path: kinds.vs.link, label: 'Go to virtual servers', uuid };
      case 'backend':
        return { path: kinds.backend.link, label: 'Go to backends', uuid };
      default:
        return null;
    }
  });

  private vsHealthLabel(vs: VirtualServer): string {
    const health = this.instanceHealth.health(vs);
    if (!health) {
      return 'Loading state ...';
    }
    if (health.total === 0) {
      return 'No endpoint reported';
    }
    return `${health.healthy}/${health.total} endpoints are healthy`;
  }

  private routeRows(node: GraphNode): Row[] {
    if (node.route) {
      return [
        { label: 'Domains', value: node.title },
        { label: 'Match', value: routeMatchLabel(node.route) },
        { label: 'Action', value: routeActionLabel(node.route) },
      ];
    }
    const vs = node.resource as VirtualServer;
    if (vs.routeConfig.case === 'tlsTcpRoutesConfig') {
      return [
        { label: 'Server names', value: node.title },
        { label: 'TLS', value: node.subtitle },
      ];
    }
    return [{ label: 'Match', value: node.subtitle }];
  }
}
