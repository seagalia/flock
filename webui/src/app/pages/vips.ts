import { Component, inject } from '@angular/core';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { Api, ListPage } from '../core/api';
import { KindName } from '../core/resources';
import { LockBadge } from '../edit/lock-badge';
import { ResourceEditor } from '../edit/resource-editor';
import { LinksPermissions } from '../ui/links-permissions';
import { Column, ExpandedRow, ResourceTable } from '../ui/resource-table';

@Component({
  selector: 'flock-vips',
  imports: [
    ResourceEditor,
    LockBadge,
    MatTableModule,
    MatDividerModule,
    ResourceTable,
    ExpandedRow,
    LinksPermissions,
  ],
  template: `
    <h1>VIPs Management</h1>
    <mat-divider />
    <flock-resource-table #table [columns]="columns" [load]="load" [extraItems]="editor.menuItems">
      <flock-resource-editor tableControlsEnd #editor kind="vip" (changed)="table.reload()" />
      <ng-container matColumnDef="uuid">
        <th mat-header-cell *matHeaderCellDef>UUID</th>
        <td mat-cell *matCellDef="let vip">{{ vip.UUID }}</td>
      </ng-container>
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let vip">{{ vip.name }}<flock-lock-badge [resource]="vip" /></td>
      </ng-container>
      <ng-container matColumnDef="ip">
        <th mat-header-cell *matHeaderCellDef>IP</th>
        <td mat-cell *matCellDef="let vip">
          {{ vip.ip }}{{ vip.prefixLength ? '/' + vip.prefixLength : '' }}
        </td>
      </ng-container>
      <ng-template flockExpandedRow let-vip>
        <flock-links-permissions [resource]="vip" [linkedKinds]="linkedKinds" />
      </ng-template>
    </flock-resource-table>
  `,
})
export class Vips {
  private api = inject(Api);
  protected readonly columns: Column[] = [
    { key: 'uuid', label: 'UUID' },
    { key: 'name', label: 'Name' },
    { key: 'ip', label: 'IP' },
  ];
  protected readonly linkedKinds: KindName[] = ['instance', 'vs'];
  protected load = (page: ListPage) => this.api.list('vip', page);
}
