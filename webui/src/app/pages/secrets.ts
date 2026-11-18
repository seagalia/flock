import { Component, inject } from '@angular/core';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { Secret } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { Api, ListPage } from '../core/api';
import { KindName, usedByKinds } from '../core/resources';
import { LockBadge } from '../edit/lock-badge';
import { ResourceEditor } from '../edit/resource-editor';
import { LinksPermissions } from '../ui/links-permissions';
import { Column, ExpandedRow, ResourceTable } from '../ui/resource-table';

// Secrets hold the values the metadata of other resources points at. A row only knows the keys, the values come
// from Reveal of the ⋮ menu.
@Component({
  selector: 'flock-secrets',
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
    <h1>Secrets Management</h1>
    <mat-divider />
    <flock-resource-table
      #table
      [columns]="columns"
      [load]="load"
      [selectable]="false"
      [extraItems]="editor.menuItems"
    >
      <flock-resource-editor tableControlsEnd #editor kind="secret" (changed)="table.reload()" />
      <ng-container matColumnDef="uuid">
        <th mat-header-cell *matHeaderCellDef>UUID</th>
        <td mat-cell *matCellDef="let secret">{{ secret.UUID }}</td>
      </ng-container>
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let secret">
          {{ secret.name }}<flock-lock-badge [resource]="secret" />
        </td>
      </ng-container>
      <ng-container matColumnDef="keys">
        <th mat-header-cell *matHeaderCellDef>Keys</th>
        <td mat-cell *matCellDef="let secret">{{ keys(secret) }}</td>
      </ng-container>
      <ng-template flockExpandedRow let-secret>
        <flock-links-permissions [resource]="secret" [linkedKinds]="linkedKinds" />
      </ng-template>
    </flock-resource-table>
  `,
})
export class Secrets {
  protected readonly columns: Column[] = [
    { key: 'uuid', label: 'UUID' },
    { key: 'name', label: 'Name' },
    { key: 'keys', label: 'Keys' },
  ];
  protected readonly linkedKinds: KindName[] = usedByKinds.secret;
  private api = inject(Api);

  protected load = (page: ListPage) => this.api.list('secret', page);

  // only the keys are drawn, whatever the answer holds
  protected keys(secret: Secret): string {
    return Object.keys(secret.data).sort().join(', ');
  }
}
