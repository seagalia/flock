import { Component, inject } from '@angular/core';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { IpPool, IpPool_Privacy, Zone } from '../../gen/flock/api/config/zone/v1/zone_pb';
import { Api, ListPage } from '../core/api';
import { defaultPool, metadataLabels } from '../core/display';
import { LockBadge } from '../edit/lock-badge';
import { ResourceEditor } from '../edit/resource-editor';
import { Column, ExpandedRow, ResourceTable } from '../ui/resource-table';

// Zones are where instances run, they have no linked resources or permissions of their own.
// A zone can have many pools: the row only counts them, the expanded row details them.
@Component({
  selector: 'flock-zones',
  imports: [
    ResourceEditor,
    LockBadge,
    MatTableModule,
    MatDividerModule,
    ResourceTable,
    ExpandedRow,
  ],
  template: `
    <h1>Zones Management</h1>
    <mat-divider />
    <flock-resource-table
      #table
      [columns]="columns"
      [hiddenColumns]="[]"
      [load]="load"
      [extraItems]="editor.menuItems"
    >
      <flock-resource-editor tableControlsEnd #editor kind="zone" (changed)="table.reload()" />
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let zone">
          {{ zone.name }}<flock-lock-badge [resource]="zone" />
        </td>
      </ng-container>
      <ng-container matColumnDef="zone">
        <th mat-header-cell *matHeaderCellDef>Zone</th>
        <td mat-cell *matCellDef="let zone">{{ zone.zone }}</td>
      </ng-container>
      <ng-container matColumnDef="pools">
        <th mat-header-cell *matHeaderCellDef>Pools</th>
        <td mat-cell *matCellDef="let zone">
          @if (zone.pools.length > 0) {
            {{ zone.pools.length }} (default: {{ defaultPoolOf(zone)?.name ?? 'none' }})
          } @else {
            Unmanaged
          }
        </td>
      </ng-container>
      <ng-template flockExpandedRow let-zone>
        <h2>Pools</h2>
        <mat-divider />
        @if (zone.pools.length > 0) {
          <div class="bordered">
            <table mat-table [dataSource]="zone.pools">
              <ng-container matColumnDef="name">
                <th mat-header-cell *matHeaderCellDef>Name</th>
                <td mat-cell *matCellDef="let pool">
                  <strong>{{ pool.name }}</strong>
                  @if (pool === defaultPoolOf(zone)) {
                    <em>(default)</em>
                  }
                </td>
              </ng-container>
              <ng-container matColumnDef="privacy">
                <th mat-header-cell *matHeaderCellDef>Privacy</th>
                <td mat-cell *matCellDef="let pool">{{ privacy(pool) }}</td>
              </ng-container>
              <ng-container matColumnDef="metadata">
                <th mat-header-cell *matHeaderCellDef>Metadata</th>
                <td mat-cell *matCellDef="let pool">
                  @for (label of metadataOf(pool); track label) {
                    <div>{{ label }}</div>
                  }
                </td>
              </ng-container>
              <ng-container matColumnDef="ips">
                <th mat-header-cell *matHeaderCellDef>Subnets and ranges</th>
                <td mat-cell *matCellDef="let pool">
                  @for (subnet of pool.subnets; track $index) {
                    <div>{{ subnet.ip }}/{{ subnet.prefixLength }}</div>
                  }
                  @for (ipRange of pool.ipRanges; track $index) {
                    <div>{{ ipRange.ipMin }} - {{ ipRange.ipMax }}</div>
                  }
                </td>
              </ng-container>
              <tr mat-header-row *matHeaderRowDef="poolColumns"></tr>
              <tr mat-row *matRowDef="let row; columns: poolColumns"></tr>
            </table>
          </div>
        } @else {
          <p>This zone has no pools: vip ips are not managed by the ipam.</p>
        }
      </ng-template>
    </flock-resource-table>
  `,
  styles: `
    .bordered {
      border: 1px solid var(--flock-border);
      border-radius: 8px;
      overflow: auto;
      margin: 10px 0 20px;
    }
  `,
})
export class Zones {
  protected readonly columns: Column[] = [
    { key: 'name', label: 'Name' },
    { key: 'zone', label: 'Zone' },
    { key: 'pools', label: 'Pools' },
  ];
  protected readonly poolColumns = ['name', 'privacy', 'metadata', 'ips'];
  private api = inject(Api);

  protected load = (page: ListPage) => this.api.list('zone', page);
  protected defaultPoolOf = (zone: Zone) => defaultPool(zone.pools);
  protected metadataOf = (pool: IpPool) => metadataLabels(pool.metadata);
  protected privacy = (pool: IpPool) =>
    pool.privacy === IpPool_Privacy.PRIVATE ? 'Private' : 'Public';
}
