import { Component, inject } from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api, ListPage } from '../core/api';
import { acceptsProxyProtocol, vsType } from '../core/display';
import { KindName } from '../core/resources';
import { LockBadge } from '../edit/lock-badge';
import { ResourceEditor } from '../edit/resource-editor';
import { LinksPermissions } from '../ui/links-permissions';
import { Column, ExpandedRow, ResourceTable } from '../ui/resource-table';
import { VirtualHosts } from '../ui/virtual-hosts';

@Component({
  selector: 'flock-virtual-servers',
  imports: [
    ResourceEditor,
    LockBadge,
    MatTableModule,
    MatCardModule,
    MatDividerModule,
    ResourceTable,
    ExpandedRow,
    LinksPermissions,
    VirtualHosts,
  ],
  template: `
    <h1>Virtual Servers Management</h1>
    <mat-divider />
    <flock-resource-table #table [columns]="columns" [load]="load" [extraItems]="editor.menuItems">
      <flock-resource-editor tableControlsEnd #editor kind="vs" (changed)="table.reload()" />
      <ng-container matColumnDef="uuid">
        <th mat-header-cell *matHeaderCellDef>UUID</th>
        <td mat-cell *matCellDef="let vs">{{ vs.UUID }}</td>
      </ng-container>
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let vs">{{ vs.name }}<flock-lock-badge [resource]="vs" /></td>
      </ng-container>
      <ng-container matColumnDef="port">
        <th mat-header-cell *matHeaderCellDef>Port</th>
        <td mat-cell *matCellDef="let vs">{{ vs.port }}</td>
      </ng-container>
      <ng-container matColumnDef="type">
        <th mat-header-cell *matHeaderCellDef>Type</th>
        <td mat-cell *matCellDef="let vs">{{ type(vs) }}</td>
      </ng-container>
      <ng-template flockExpandedRow let-vs>
        @let acceptsProxy = acceptsProxyProtocol(vs);
        @if (acceptsProxy !== null) {
          <h2>Configuration</h2>
          <mat-divider />
          <div class="cards">
            <mat-card appearance="outlined">
              <mat-card-header>
                <mat-card-subtitle>Accept Proxy Protocol</mat-card-subtitle>
                <mat-card-title [class]="acceptsProxy ? 'good' : 'bad'">{{
                  acceptsProxy ? 'Enabled' : 'Disabled'
                }}</mat-card-title>
              </mat-card-header>
            </mat-card>
          </div>
        }
        @if (isHttpRoutes(vs)) {
          <flock-virtual-hosts [virtualHosts]="virtualHosts(vs)" />
        }
        <flock-links-permissions [resource]="vs" [linkedKinds]="linkedKinds" />
      </ng-template>
    </flock-resource-table>
  `,
  styles: `
    .good {
      color: var(--flock-ok-text);
    }
    .bad {
      color: var(--flock-error-text);
    }
  `,
})
export class VirtualServers {
  protected readonly columns: Column[] = [
    { key: 'uuid', label: 'UUID' },
    { key: 'name', label: 'Name' },
    { key: 'port', label: 'Port' },
    { key: 'type', label: 'Type' },
  ];
  protected readonly linkedKinds: KindName[] = ['backend', 'vip', 'certificate'];
  private api = inject(Api);

  protected load = (page: ListPage) => this.api.list('vs', page);
  protected type = (vs: VirtualServer) => vsType(vs);
  protected acceptsProxyProtocol = acceptsProxyProtocol;
  protected isHttpRoutes = (vs: VirtualServer) => vs.routeConfig.case === 'httpRoutesConfig';
  protected virtualHosts = (vs: VirtualServer) => {
    if (vs.routeConfig.case === 'httpRoutesConfig') {
      return vs.routeConfig.value.virtualHosts;
    }
    return [];
  };
}
