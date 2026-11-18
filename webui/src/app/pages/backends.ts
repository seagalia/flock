import { Component, inject } from '@angular/core';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { Backend } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { Api, ListPage } from '../core/api';
import { backendType, healthCheckType, lbPolicyLabel } from '../core/display';
import { KindName } from '../core/resources';
import { LockBadge } from '../edit/lock-badge';
import { ResourceEditor } from '../edit/resource-editor';
import { BackendEndpoints, HealthCheckConfig } from '../ui/backend-config';
import { LinksPermissions } from '../ui/links-permissions';
import { Column, ExpandedRow, ResourceTable } from '../ui/resource-table';

@Component({
  selector: 'flock-backends',
  imports: [
    ResourceEditor,
    LockBadge,
    MatTableModule,
    MatDividerModule,
    ResourceTable,
    ExpandedRow,
    LinksPermissions,
    BackendEndpoints,
    HealthCheckConfig,
  ],
  template: `
    <h1>Backends Management</h1>
    <mat-divider />
    <flock-resource-table #table [columns]="columns" [load]="load" [extraItems]="editor.menuItems">
      <flock-resource-editor tableControlsEnd #editor kind="backend" (changed)="table.reload()" />
      <ng-container matColumnDef="uuid">
        <th mat-header-cell *matHeaderCellDef>UUID</th>
        <td mat-cell *matCellDef="let backend">{{ backend.UUID }}</td>
      </ng-container>
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let backend">
          {{ backend.name }}<flock-lock-badge [resource]="backend" />
        </td>
      </ng-container>
      <ng-container matColumnDef="type">
        <th mat-header-cell *matHeaderCellDef>Type</th>
        <td mat-cell *matCellDef="let backend">{{ type(backend) }}</td>
      </ng-container>
      <ng-container matColumnDef="policy">
        <th mat-header-cell *matHeaderCellDef>LB Policy</th>
        <td mat-cell *matCellDef="let backend">{{ policy(backend) }}</td>
      </ng-container>
      <ng-container matColumnDef="endpoint">
        <th mat-header-cell *matHeaderCellDef>Endpoint Configuration</th>
        <td mat-cell *matCellDef="let backend">
          <flock-backend-endpoints [backend]="backend" />
        </td>
      </ng-container>
      <ng-container matColumnDef="hctype">
        <th mat-header-cell *matHeaderCellDef>HealthCheck Type</th>
        <td mat-cell *matCellDef="let backend">{{ hcType(backend) }}</td>
      </ng-container>
      <ng-template flockExpandedRow let-backend>
        <h2>HealthCheck</h2>
        <mat-divider />
        <flock-healthcheck-config [healthCheck]="backend.healthCheck" />
        <flock-links-permissions [resource]="backend" [linkedKinds]="linkedKinds" />
      </ng-template>
    </flock-resource-table>
  `,
})
export class Backends {
  protected readonly columns: Column[] = [
    { key: 'uuid', label: 'UUID' },
    { key: 'name', label: 'Name' },
    { key: 'type', label: 'Type' },
    { key: 'policy', label: 'LB Policy' },
    { key: 'endpoint', label: 'Endpoint Configuration' },
    { key: 'hctype', label: 'HealthCheck Type' },
  ];
  protected readonly linkedKinds: KindName[] = ['vs'];
  private api = inject(Api);

  protected load = (page: ListPage) => this.api.list('backend', page);
  protected type = (backend: Backend) => backendType(backend);
  protected policy = (backend: Backend) => lbPolicyLabel(backend);
  protected hcType = (backend: Backend) => healthCheckType(backend.healthCheck);
}
