import { Component, effect, inject, input, signal } from '@angular/core';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { RouterLink } from '@angular/router';
import {
  MemberPermission,
  MemberType,
  Role,
} from '../../gen/flock/api/config/permission/v1/permission_pb';
import { Api } from '../core/api';
import { Kind, kindOf, kinds, KindName, Resource, resourceId, typeUrl } from '../core/resources';
import { usedBy } from '../edit/used-by';
import { ResourceMenu } from './resource-menu';

interface LinkedResource {
  kind: Kind;
  resource: Resource;
}

const memberTypeLabels: Record<MemberType, string> = {
  [MemberType.USER]: 'User',
  [MemberType.GROUP]: 'Group',
};

const roleLabels: Record<Role, string> = {
  [Role.READER]: 'Reader',
  [Role.WRITER]: 'Writer',
  [Role.OWNER]: 'Owner',
};

// LinksPermissions closes every expanded row: who can use the resource and what uses it
@Component({
  selector: 'flock-links-permissions',
  imports: [MatTableModule, MatDividerModule, RouterLink, ResourceMenu],
  template: `
    <h2>Permissions</h2>
    <mat-divider />
    <p class="allow-all-read">
      {{
        allowAllRead()
          ? 'Every user can read this resource.'
          : 'Only the members below can read this resource.'
      }}
    </p>
    @if (permissions().length > 0) {
      <div class="bordered">
        <table mat-table [dataSource]="permissions()">
          <ng-container matColumnDef="member_name">
            <th mat-header-cell *matHeaderCellDef>Member Name</th>
            <td mat-cell *matCellDef="let perm">{{ perm.member?.memberName }}</td>
          </ng-container>
          <ng-container matColumnDef="member_type">
            <th mat-header-cell *matHeaderCellDef>Member Type</th>
            <td mat-cell *matCellDef="let perm">{{ memberTypeLabel(perm) }}</td>
          </ng-container>
          <ng-container matColumnDef="role">
            <th mat-header-cell *matHeaderCellDef>Role</th>
            <td mat-cell *matCellDef="let perm">
              <span [class]="'chip role-' + roleLabel(perm).toLowerCase()">{{
                roleLabel(perm)
              }}</span>
            </td>
          </ng-container>
          <ng-container matColumnDef="actions">
            <th mat-header-cell *matHeaderCellDef>actions</th>
            <td mat-cell *matCellDef="let perm"><flock-resource-menu [resource]="perm" /></td>
          </ng-container>
          <tr mat-header-row *matHeaderRowDef="permColumns"></tr>
          <tr mat-row *matRowDef="let row; columns: permColumns"></tr>
        </table>
      </div>
    } @else {
      <p>This resource has no permission set.</p>
    }

    <h2>Linked Resources</h2>
    <mat-divider />
    @if (linked().length > 0) {
      <div class="bordered">
        <table mat-table [dataSource]="linked()">
          <ng-container matColumnDef="type">
            <th mat-header-cell *matHeaderCellDef>Type</th>
            <td mat-cell *matCellDef="let link">{{ link.kind.label }}</td>
          </ng-container>
          <ng-container matColumnDef="name">
            <th mat-header-cell *matHeaderCellDef>Name</th>
            <td mat-cell *matCellDef="let link">
              <a [routerLink]="link.kind.link" [queryParams]="{ uuid: idOf(link) }">{{
                link.resource.name
              }}</a>
            </td>
          </ng-container>
          <ng-container matColumnDef="uuid">
            <th mat-header-cell *matHeaderCellDef>UUID</th>
            <td mat-cell *matCellDef="let link">
              <a [routerLink]="link.kind.link" [queryParams]="{ uuid: idOf(link) }">{{
                idOf(link)
              }}</a>
            </td>
          </ng-container>
          <tr mat-header-row *matHeaderRowDef="linkColumns"></tr>
          <tr mat-row *matRowDef="let row; columns: linkColumns"></tr>
        </table>
      </div>
    } @else {
      <p>This resource is not used by anything.</p>
    }
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
export class LinksPermissions {
  readonly resource = input.required<Resource>();
  readonly linkedKinds = input.required<KindName[]>();

  private api = inject(Api);
  protected permissions = signal<MemberPermission[]>([]);
  protected allowAllRead = signal(false);
  protected linked = signal<LinkedResource[]>([]);
  protected readonly permColumns = ['member_name', 'member_type', 'role', 'actions'];
  protected readonly linkColumns = ['type', 'name', 'uuid'];

  constructor() {
    effect(() => {
      const resource = this.resource();
      this.api.permission
        .listMemberPermissions({
          resourceType: typeUrl(kindOf(resource)),
          resourceUuid: resourceId(resource),
          seeAll: true,
        })
        .then((resp) => {
          this.permissions.set(resp.memberPermissions);
          this.allowAllRead.set(resp.allowAllRead);
        });
    });
    effect(() => {
      const resource = this.resource();
      usedBy(this.api, resource, this.linkedKinds()).then((all) =>
        this.linked.set(all.map((link) => ({ kind: kinds[link.kind], resource: link.resource }))),
      );
    });
  }

  protected memberTypeLabel(perm: MemberPermission): string {
    return memberTypeLabels[perm.member?.memberType ?? MemberType.USER];
  }

  protected roleLabel(perm: MemberPermission): string {
    return roleLabels[perm.role] ?? 'Unknown';
  }

  protected idOf(link: LinkedResource): string {
    return resourceId(link.resource);
  }
}
