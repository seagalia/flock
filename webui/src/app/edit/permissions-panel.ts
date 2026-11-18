import {
  Component,
  computed,
  effect,
  inject,
  input,
  output,
  signal,
  untracked,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelect, MatSelectChange, MatSelectModule } from '@angular/material/select';
import { MatSlideToggleChange, MatSlideToggleModule } from '@angular/material/slide-toggle';
import { ConnectError } from '@connectrpc/connect';
import {
  Member,
  MemberPermission,
  MemberType,
  Role,
} from '../../gen/flock/api/config/permission/v1/permission_pb';
import { Api, silentCall } from '../core/api';
import { kindOf, Resource, resourceId, typeUrl } from '../core/resources';
import { SidePanel } from '../ui/side-panel';
import { Access, RoleCache } from './access';

// highest role first, the owners are what matters most
const rolesShown: { role: Role; label: string }[] = [
  { role: Role.OWNER, label: 'Owner' },
  { role: Role.WRITER, label: 'Writer' },
  { role: Role.READER, label: 'Reader' },
];

// PermissionsPanel lists who holds which role on a resource and changes it. Only an owner (or an admin) may change
// anything, which is known from a permission call on the first click, never when the panel opens.
@Component({
  selector: 'flock-permissions-panel',
  imports: [
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatSlideToggleModule,
    SidePanel,
  ],
  styleUrl: './permissions-panel.scss',
  template: `
    <flock-side-panel title="Permissions" [subject]="resource().name" (closed)="closed.emit()">
      @if (denial()) {
        <p class="reason" role="status">{{ denial() }}</p>
      }
      @if (problem()) {
        <p class="problem" role="alert">{{ problem() }}</p>
      }

      <mat-slide-toggle
        class="allow-all-read"
        [checked]="allowAllRead()"
        [disabled]="!!denial() || busy()"
        (change)="setAllowAllRead($event)"
      >
        Every user can read this resource
      </mat-slide-toggle>

      @for (group of groups(); track group.label) {
        <section [attr.data-role]="group.label">
          <h3>{{ group.label }}</h3>
          @for (perm of group.members; track memberKey(perm)) {
            <div class="member">
              <span class="name">{{ perm.member?.memberName }}</span>
              <span class="type">{{ typeLabel(perm.member) }}</span>
              <mat-form-field class="role-field" subscriptSizing="dynamic">
                <mat-select
                  [attr.data-member]="perm.member?.memberName"
                  [aria-label]="'Role of ' + perm.member?.memberName"
                  [value]="perm.role"
                  [disabled]="!!denial() || busy()"
                  (selectionChange)="changeRole(perm, $event)"
                >
                  @for (option of rolesShown; track option.role) {
                    <mat-option [value]="option.role">{{ option.label }}</mat-option>
                  }
                </mat-select>
              </mat-form-field>
              <button
                mat-icon-button
                type="button"
                [attr.aria-label]="'Remove ' + perm.member?.memberName"
                [disabled]="!!denial() || busy()"
                (click)="remove(perm)"
              >
                <mat-icon>delete</mat-icon>
              </button>
            </div>
          }
        </section>
      }

      <section class="add">
        <h3>Add a member</h3>
        <div class="member">
          <mat-form-field class="name-field" subscriptSizing="dynamic">
            <mat-label>User or group name</mat-label>
            <input
              matInput
              class="member-name"
              [value]="newName()"
              [disabled]="!!denial() || busy()"
              (input)="newName.set($any($event.target).value)"
            />
          </mat-form-field>
          <mat-form-field class="type-field" subscriptSizing="dynamic">
            <mat-label>Type</mat-label>
            <mat-select
              class="member-type"
              [value]="newType()"
              [disabled]="!!denial() || busy()"
              (selectionChange)="newType.set($event.value)"
            >
              <mat-option [value]="MemberType.USER">User</mat-option>
              <mat-option [value]="MemberType.GROUP">Group</mat-option>
            </mat-select>
          </mat-form-field>
          <mat-form-field class="role-field" subscriptSizing="dynamic">
            <mat-label>Role</mat-label>
            <mat-select
              class="member-role"
              [value]="newRole()"
              [disabled]="!!denial() || busy()"
              (selectionChange)="newRole.set($event.value)"
            >
              @for (option of rolesShown; track option.role) {
                <mat-option [value]="option.role">{{ option.label }}</mat-option>
              }
            </mat-select>
          </mat-form-field>
          <button
            mat-flat-button
            type="button"
            class="add"
            [disabled]="!!denial() || busy() || !newName().trim()"
            (click)="add()"
          >
            <mat-icon>add</mat-icon>
            Add
          </button>
        </div>
      </section>
    </flock-side-panel>
  `,
})
export class PermissionsPanel {
  readonly resource = input.required<Resource>();
  // shared with the resource panel when it has one, so a denial found by either is not asked twice
  readonly cache = input<RoleCache>(new Map());
  readonly closed = output<void>();
  // the page lists the permissions in its expanded rows, it lists them again after a change
  readonly changed = output<void>();

  private api = inject(Api);
  private access = inject(Access);

  protected readonly MemberType = MemberType;
  protected readonly rolesShown = rolesShown;
  protected readonly members = signal<MemberPermission[]>([]);
  protected readonly allowAllRead = signal(false);
  protected readonly denial = signal('');
  protected readonly problem = signal('');
  protected readonly busy = signal(false);
  protected readonly newName = signal('');
  protected readonly newType = signal(MemberType.USER);
  protected readonly newRole = signal(Role.READER);
  protected readonly groups = computed(() =>
    rolesShown.map(({ role, label }) => ({
      label,
      members: this.members().filter((perm) => perm.role === role),
    })),
  );

  private allowed = false;

  constructor() {
    // The body is untracked because load reads the resource input too, and the effect must rerun only when the
    // resource changes.
    effect(() => {
      this.resource();
      untracked(() =>
        this.load().catch((err) => this.problem.set(ConnectError.from(err).rawMessage)),
      );
    });
  }

  protected typeLabel(member: Member | undefined): string {
    return member?.memberType === MemberType.GROUP ? 'Group' : 'User';
  }

  // the key holds the type too, a user and a group may share a name
  protected memberKey(perm: MemberPermission): string {
    return `${perm.member?.memberType}:${perm.member?.memberName}`;
  }

  protected async changeRole(perm: MemberPermission, event: MatSelectChange) {
    const done = await this.change(async () => {
      await this.api.permission.addPermission(
        { ...this.target(), Role: event.value, member: perm.member },
        silentCall,
      );
    });
    // a refused change must not leave the select showing the role the user just picked
    if (!done) {
      (event.source as MatSelect).value = perm.role;
    }
  }

  protected async remove(perm: MemberPermission) {
    await this.change(async () => {
      await this.api.permission.deletePermission(
        { ...this.target(), member: perm.member },
        silentCall,
      );
    });
  }

  protected async add() {
    const member = { memberName: this.newName().trim(), memberType: this.newType() };
    const added = await this.change(async () => {
      await this.api.permission.addPermission(
        { ...this.target(), Role: this.newRole(), member },
        silentCall,
      );
    });
    if (added) {
      this.newName.set('');
    }
  }

  protected async setAllowAllRead(event: MatSlideToggleChange) {
    const done = await this.change(async () => {
      await this.api.permission.setAllowAllRead(
        { ...this.target(), allowAllRead: event.checked },
        silentCall,
      );
    });
    // a refused change must not leave the switch showing the state the user just chose
    if (!done) {
      event.source.checked = this.allowAllRead();
    }
  }

  private target() {
    const resource = this.resource();
    return { resourceType: typeUrl(kindOf(resource)), resourceUuid: resourceId(resource) };
  }

  private async load() {
    const response = await this.api.permission.listMemberPermissions(
      { ...this.target(), seeAll: true },
      silentCall,
    );
    this.members.set(response.memberPermissions);
    this.allowAllRead.set(response.allowAllRead);
  }

  // change runs one change after the owner check of the first click, true when it went through
  private async change(run: () => Promise<void>): Promise<boolean> {
    if (this.busy() || this.denial()) {
      return false;
    }
    this.busy.set(true);
    this.problem.set('');
    try {
      if (!this.allowed) {
        const denial = await this.access.check(
          this.resource(),
          Role.OWNER,
          this.cache(),
          'Changing permissions',
        );
        if (denial) {
          this.denial.set(denial.reason);
          return false;
        }
        this.allowed = true;
      }
      await run();
      await this.load();
      this.changed.emit();
      return true;
    } catch (err) {
      this.problem.set(ConnectError.from(err).rawMessage);
      await this.load().catch(() => undefined);
      return false;
    } finally {
      this.busy.set(false);
    }
  }
}
