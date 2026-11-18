import { Component, inject, signal } from '@angular/core';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { RouterLink } from '@angular/router';
import { Instance } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { Api, ListPage, silentCall } from '../core/api';
import { KindName, kinds, Resource } from '../core/resources';
import { LockBadge } from '../edit/lock-badge';
import { ResourceEditor } from '../edit/resource-editor';
import { AgentHealthChip } from '../ui/agent-health-chip';
import { AgentsTable } from '../ui/agents-table';
import { LinksPermissions } from '../ui/links-permissions';
import { Column, ExpandedRow, ResourceTable } from '../ui/resource-table';

@Component({
  selector: 'flock-instances',
  imports: [
    ResourceEditor,
    LockBadge,
    MatTableModule,
    MatDividerModule,
    RouterLink,
    ResourceTable,
    ExpandedRow,
    LinksPermissions,
    AgentsTable,
    AgentHealthChip,
  ],
  template: `
    <h1>Instances Management</h1>
    <mat-divider />
    <flock-resource-table #table [columns]="columns" [load]="load" [extraItems]="editor.menuItems">
      <flock-resource-editor tableControlsEnd #editor kind="instance" (changed)="table.reload()" />
      <ng-container matColumnDef="uuid">
        <th mat-header-cell *matHeaderCellDef>UUID</th>
        <td mat-cell *matCellDef="let instance">{{ instance.UUID }}</td>
      </ng-container>
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let instance">
          {{ instance.name }}<flock-lock-badge [resource]="instance" />
        </td>
      </ng-container>
      <ng-container matColumnDef="zone">
        <th mat-header-cell *matHeaderCellDef>Zone</th>
        <td mat-cell *matCellDef="let instance">
          <a
            [routerLink]="zoneLink"
            [queryParams]="{ uuid: instance.zoneUuid }"
            (click)="$event.stopPropagation()"
            >{{ zoneNames().get(instance.zoneUuid) ?? instance.zoneUuid }}</a
          >
        </td>
      </ng-container>
      <ng-container matColumnDef="resourceType">
        <th mat-header-cell *matHeaderCellDef>Resource Type</th>
        <td mat-cell *matCellDef="let instance">{{ instance.resourceType }}</td>
      </ng-container>
      <ng-container matColumnDef="agents">
        <th mat-header-cell *matHeaderCellDef>Agents</th>
        <td mat-cell *matCellDef="let instance">
          <flock-agent-health-chip [instance]="instance" />
        </td>
      </ng-container>
      <ng-template flockExpandedRow let-instance>
        <flock-agents-table [instance]="instance" />
        <flock-links-permissions [resource]="instance" [linkedKinds]="linkedKinds" />
      </ng-template>
    </flock-resource-table>
  `,
})
export class Instances {
  private api = inject(Api);
  protected readonly columns: Column[] = [
    { key: 'uuid', label: 'UUID' },
    { key: 'name', label: 'Name' },
    { key: 'zone', label: 'Zone' },
    { key: 'resourceType', label: 'Resource Type' },
    { key: 'agents', label: 'Agents' },
  ];
  protected readonly linkedKinds: KindName[] = ['vip'];
  protected readonly zoneLink = kinds.zone.link;
  // an instance only knows the uuid of its zone, the names are read for the zones of the page shown
  protected zoneNames = signal(new Map<string, string>());

  protected load = async (page: ListPage): Promise<Resource[]> => {
    const instances = (await this.api.list('instance', page)) as Instance[];
    await this.readZoneNames(instances);
    return instances;
  };

  private async readZoneNames(instances: Instance[]) {
    const known = this.zoneNames();
    const zoneUuids = new Set(instances.map((instance) => instance.zoneUuid));
    const missing = [...zoneUuids].filter((uuid) => !known.has(uuid));
    // a zone that can't be read keeps showing its uuid
    const read = await Promise.allSettled(
      missing.map((uuid) => this.api.zone.readZone({ UUID: uuid }, silentCall)),
    );
    const names = new Map(known);
    for (const result of read) {
      if (result.status === 'fulfilled' && result.value.zone) {
        names.set(result.value.zone.UUID, result.value.zone.name);
      }
    }
    this.zoneNames.set(names);
  }
}
