import { Clipboard } from '@angular/cdk/clipboard';
import { Component, computed, inject, input, OnInit, output, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Role } from '../../gen/flock/api/config/permission/v1/permission_pb';
import { Secret } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { Api } from '../core/api';
import { Notifier } from '../core/notifier';
import { KindName, kinds, kindWord, Resource, resourceId } from '../core/resources';
import { Session } from '../core/session';
import { SecretReveal } from '../ui/secret-reveal';
import { confirm } from '../ui/confirm-dialog';
import { ErrorDialog, ErrorDialogData } from '../ui/error-dialog';
import { MenuItem } from '../ui/resource-menu';
import { Access, adminReason, ControllerFeatures } from './access';
import { kindRules } from './kind-rules';
import { isLocked, lockReason } from './lock';
import { calls, Editable, editMenuItems, ResourcePanel } from './resource-panel';
import { usedBy } from './used-by';
import { VipAttach } from './vip-attach';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';

// What the panel shows, a stored resource or a Create with its prefill
interface Shown {
  resource: Resource | null;
  prefill: Resource | null;
}

// ResourceEditor is the Create button of a resource page and the panel it opens. The page puts it in the table
// controls and adds menuItems to the ⋮ menu of the rows, where Edit opens a row in the panel and Delete removes it.
@Component({
  selector: 'flock-resource-editor',
  imports: [MatButtonModule, MatIconModule, MatTooltipModule, ResourcePanel, SecretReveal],
  template: `
    @if (!linking() && createButton()) {
      <button
        mat-flat-button
        type="button"
        class="create"
        [disabled]="!!createGreyed()"
        disabledInteractive
        [matTooltip]="createGreyed()"
        (click)="create(null)"
      >
        <mat-icon>add</mat-icon>
        Create
      </button>
    }
    @if (shown(); as panel) {
      <flock-resource-panel
        [kind]="kind()"
        [resource]="panel.resource"
        [prefill]="panel.prefill"
        [rules]="rules()"
        (changed)="changed.emit()"
        (created)="onCreated($event)"
        (closed)="onClosed()"
      />
    }
    @if (revealed(); as secret) {
      <flock-secret-reveal [secret]="secret" (closed)="revealed.set(null)" />
    }
  `,
})
export class ResourceEditor implements OnInit {
  readonly kind = input.required<KindName>();
  // True when the shell opens it for a picker creating the resource it links (ResourceCreations). It opens its Create
  // panel at once and closes it once the resource is created, so the user is back in the form they came from.
  readonly linking = input(false);
  // false for a page that only edits and deletes resources of this kind, such as the Overview from its config map
  readonly createButton = input(true);
  // a resource was created, saved or deleted, the page lists its resources again
  readonly changed = output<void>();
  readonly created = output<Resource>();
  // the panel of a linking editor closed, created or not
  readonly finished = output<void>();

  private api = inject(Api);
  private access = inject(Access);
  private dialog = inject(MatDialog);
  private session = inject(Session);
  private clipboard = inject(Clipboard);
  private notifier = inject(Notifier);
  private vipAttach = inject(VipAttach);

  protected readonly shown = signal<Shown | null>(null);
  // the secret with its values, held only while its panel is open
  protected readonly revealed = signal<Secret | null>(null);
  private readonly features = signal<ControllerFeatures>({ ipamEnabled: false });
  private readonly isAdmin = computed(() => this.session.user()?.isAdmin ?? false);

  protected readonly rules = computed(() =>
    kindRules(this.kind(), {
      api: this.api,
      dialog: this.dialog,
      isAdmin: this.isAdmin(),
      features: this.features(),
    }),
  );
  protected readonly createGreyed = computed(() =>
    adminReason(this.kind(), 'create', this.features(), this.isAdmin()),
  );

  constructor() {
    // until the features are read the ipam counts as off, a non-admin's VIP Create is greyed for that moment
    this.access
      .features()
      .then((features) => this.features.set(features))
      .catch(() => undefined);
  }

  open(resource: Resource) {
    this.shown.set({ resource, prefill: null });
  }

  readonly menuItems = (row: Resource): MenuItem[] => [
    // the panel also holds the permissions and what uses the resource, so a locked resource still opens, read-only
    { label: 'Edit', icon: 'edit', tone: 'primary', run: () => this.open(row) },
    ...(this.kind() === 'vs'
      ? [
          {
            label: 'Attach to VIPs',
            icon: 'link',
            run: () => this.editVips(row as VirtualServer, false),
          },
        ]
      : []),
    ...(this.kind() === 'secret'
      ? [{ label: 'Reveal', icon: 'visibility', run: () => this.reveal(row) }]
      : []),
    {
      label: 'Delete',
      icon: 'delete',
      tone: 'danger',
      disabled: this.deleteGreyed(row) || undefined,
      run: () => this.remove(row),
    },
    ...editMenuItems(
      row,
      {
        clone: (prefill) => this.create(prefill),
        copy: (text) => {
          if (this.clipboard.copy(text)) {
            this.notifier.info(`Copied the resource file document of ${row.name}`);
          }
        },
      },
      this.createGreyed(),
    ),
  ];

  private async editVips(vs: VirtualServer, creating: boolean) {
    const changed = await this.vipAttach.edit(vs, creating);
    if (changed) {
      this.changed.emit();
    }
  }

  // reveal checks the owner role on click, a menu is built for every row, and asks for the values only then
  private async reveal(secret: Resource) {
    const denial = await this.access.check(secret, Role.OWNER, new Map(), 'Revealing');
    if (denial) {
      this.dialog.open(ErrorDialog, {
        data: {
          title: `Can't reveal the secret ${secret.name}`,
          message: denial.reason,
          links: [],
        },
      });
      return;
    }
    try {
      this.revealed.set(
        (await this.api.secret.revealSecret({ UUID: resourceId(secret) })).secret ?? null,
      );
    } catch {
      // the error dialog of the api already tells why
    }
  }

  // deleteGreyed is why Delete is refused before any call, empty when only a click can tell
  private deleteGreyed(resource: Resource): string {
    const locked = isLocked(resource as { metadata?: Record<string, string> });
    if (locked && !this.isAdmin()) {
      return `${lockReason} Only an admin can delete it anyway.`;
    }
    return adminReason(this.kind(), 'delete', this.features(), this.isAdmin());
  }

  // remove looks for what uses the resource and checks the owner role only on click, a menu is built for every row
  private async remove(resource: Resource) {
    const word = kindWord(this.kind());
    const refuse = (reason: string, links: ErrorDialogData['links'] = []) =>
      this.dialog.open(ErrorDialog, {
        data: { title: `Can't delete the ${word} ${resource.name}`, message: reason, links },
      });
    const users = await usedBy(this.api, resource).catch(() => []);
    if (users.length > 0) {
      const links = users.map((user) => ({
        label: `${kinds[user.kind].label} ${user.resource.name}`,
        path: kinds[user.kind].link,
        uuid: resourceId(user.resource),
      }));
      refuse('Used by:', links);
      return;
    }
    const denial = await this.access.check(resource, Role.OWNER, new Map());
    if (denial) {
      refuse(denial.reason);
      return;
    }
    // the controller never checks the lock, only an admin gets this far with a locked resource
    const locked = isLocked(resource as { metadata?: Record<string, string> });
    const agreed = locked
      ? await confirm(this.dialog, {
          title: `Delete the locked ${word} ${resource.name}?`,
          message: `${lockReason} Its provisioner may create it again once it is deleted.`,
          confirm: 'Delete',
          typed: resource.name,
        })
      : await confirm(this.dialog, {
          title: `Delete the ${word} ${resource.name}?`,
          message: `The ${word} ${resource.name} is deleted from the controller.`,
          confirm: 'Delete',
        });
    if (!agreed) {
      return;
    }
    try {
      await calls[this.kind() as Editable].delete(this.api, resourceId(resource));
    } catch {
      // the error dialog of the api already tells why
      return;
    }
    if (this.shown()?.resource === resource) {
      this.shown.set(null);
    }
    this.changed.emit();
  }

  ngOnInit() {
    // the panel greys its own Create, so a linking editor opens even before the features say whether it may
    if (this.linking()) {
      this.shown.set({ resource: null, prefill: null });
    }
  }

  protected create(prefill: Resource | null) {
    if (this.createGreyed()) {
      return;
    }
    this.shown.set({ resource: null, prefill });
  }

  protected onCreated(resource: Resource) {
    this.created.emit(resource);
    if (this.linking()) {
      this.onClosed();
      return;
    }
    // a virtual server serves nothing until a VIP lists it, a picker creating one leaves that to the form it came from
    if (this.kind() === 'vs') {
      void this.editVips(resource as VirtualServer, true);
    }
  }

  protected onClosed() {
    this.shown.set(null);
    if (this.linking()) {
      this.finished.emit();
    }
  }
}
