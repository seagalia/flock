import { Component, computed, inject, Injectable, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MAT_DIALOG_DATA, MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ConnectError } from '@connectrpc/connect';
import { firstValueFrom } from 'rxjs';
import { Role } from '../../gen/flock/api/config/permission/v1/permission_pb';
import { VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api, silentCall } from '../core/api';
import { listAll } from '../core/resources';
import { confirm } from '../ui/confirm-dialog';
import { ErrorDialog } from '../ui/error-dialog';
import { Access } from './access';
import type { ChainOutcome } from './delete-chain';
import { isLocked, lockReason } from './lock';

// VipChange is one VIP that starts or stops listing the virtual server
export interface VipChange {
  vip: VIP;
  attach: boolean;
}

// below this many VIPs the whole list fits in the dialog, a search field would only be noise
const searchFromVips = 10;

interface VipAttachData {
  vs: VirtualServer;
  vips: VIP[];
  // right after the Create of the virtual server, the dialog is a proposal the user may skip
  creating: boolean;
}

// VipAttachDialog lists the VIPs the user can read, the checked ones list the virtual server. It closes with the
// uuids of the checked VIPs, or with nothing when cancelled.
@Component({
  selector: 'flock-vip-attach-dialog',
  imports: [
    MatButtonModule,
    MatCheckboxModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatTooltipModule,
  ],
  template: `
    <h2 mat-dialog-title>
      {{ data.creating ? 'Attach ' + data.vs.name + ' to VIPs?' : 'VIPs of ' + data.vs.name }}
    </h2>
    <mat-dialog-content>
      @if (data.vips.length === 0) {
        <p>There is no VIP you can read.</p>
      } @else {
        <p>A VIP serves {{ data.vs.name }} on its port {{ data.vs.port }}.</p>
        <p class="count">{{ countLine() }}</p>
        @if (data.vips.length >= searchFromVips) {
          <mat-form-field class="search" subscriptSizing="dynamic">
            <mat-icon matPrefix>search</mat-icon>
            <input
              matInput
              name="search"
              placeholder="Search by name or IP"
              [value]="search()"
              (input)="search.set($any($event.target).value)"
            />
          </mat-form-field>
        }
        <ul class="vips">
          @for (vip of shown(); track vip.UUID) {
            <li>
              <mat-checkbox
                [checked]="checked().has(vip.UUID)"
                [disabled]="isLocked(vip)"
                [matTooltip]="isLocked(vip) ? lockReason : ''"
                (change)="toggle(vip.UUID)"
              >
                {{ vip.name }} <span class="detail">{{ vip.ip }}, {{ servesWord(vip) }}</span>
              </mat-checkbox>
            </li>
          } @empty {
            <li class="detail">No VIP matches {{ search() }}.</li>
          }
        </ul>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button type="button" [mat-dialog-close]="undefined">
        {{ data.creating ? 'Not now' : 'Cancel' }}
      </button>
      <button
        mat-flat-button
        type="button"
        [disabled]="!hasChanged()"
        [mat-dialog-close]="checked()"
      >
        {{ data.creating ? 'Attach' : 'Save' }}
      </button>
    </mat-dialog-actions>
  `,
  styles: `
    /* the list scrolls on its own so the search field above it stays in view */
    .vips {
      list-style: none;
      padding: 0;
      margin: 0;
      max-height: 50vh;
      overflow-y: auto;
    }
    .search {
      width: 100%;
      margin-bottom: 8px;
    }
    .count,
    .detail {
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class VipAttachDialog {
  protected data = inject<VipAttachData>(MAT_DIALOG_DATA);
  protected readonly lockReason = lockReason;
  protected readonly searchFromVips = searchFromVips;
  private readonly attached = new Set(
    this.data.vips
      .filter((vip) => vip.virtualServersUuid.includes(this.data.vs.UUID))
      .map((vip) => vip.UUID),
  );
  protected readonly checked = signal(new Set(this.attached));
  protected readonly search = signal('');
  // the VIPs attached when the dialog opened come first, sorted once so a row never moves under the pointer
  private readonly ordered = [
    ...this.data.vips.filter((vip) => this.attached.has(vip.UUID)),
    ...this.data.vips.filter((vip) => !this.attached.has(vip.UUID)),
  ];
  protected readonly shown = computed(() => {
    const search = this.search().trim().toLowerCase();
    return this.ordered.filter(
      (vip) => vip.name.toLowerCase().includes(search) || vip.ip.includes(search),
    );
  });
  protected readonly countLine = computed(() => {
    const total = this.data.vips.length;
    const vipsWord = total === 1 ? 'VIP' : 'VIPs';
    return `Attached to ${this.checked().size} of ${total} ${vipsWord}.`;
  });
  protected readonly hasChanged = computed(
    () => vipChanges(this.data.vs, this.data.vips, this.checked()).length > 0,
  );

  protected isLocked(vip: VIP): boolean {
    return isLocked(vip);
  }

  protected servesWord(vip: VIP): string {
    const count = vip.virtualServersUuid.length;
    return count === 1 ? '1 virtual server' : `${count} virtual servers`;
  }

  protected toggle(uuid: string) {
    const next = new Set(this.checked());
    if (next.has(uuid)) {
      next.delete(uuid);
    } else {
      next.add(uuid);
    }
    this.checked.set(next);
  }
}

// vipChanges are the VIPs whose list of virtual servers changes when exactly the wanted VIPs list the virtual server
export function vipChanges(vs: VirtualServer, vips: VIP[], wanted: Set<string>): VipChange[] {
  const changes: VipChange[] = [];
  for (const vip of vips) {
    const lists = vip.virtualServersUuid.includes(vs.UUID);
    const shouldList = wanted.has(vip.UUID);
    if (lists !== shouldList) {
      changes.push({ vip, attach: shouldList });
    }
  }
  return changes;
}

export function changeStep(vs: VirtualServer, change: VipChange): string {
  const target = `the VIP ${change.vip.name} (${change.vip.ip})`;
  return change.attach ? `attach ${vs.name} to ${target}` : `detach ${vs.name} from ${target}`;
}

// applyVipChanges updates the VIPs one by one. Each VIP is read again first, so a virtual server added to it meanwhile
// is kept. It stops at the first error, such as the virtual server deleted meanwhile, and nothing done is rolled back.
export async function applyVipChanges(
  api: Api,
  vs: VirtualServer,
  changes: VipChange[],
): Promise<ChainOutcome> {
  const outcome: ChainOutcome = { done: [], notDone: [], error: '' };
  for (const [index, change] of changes.entries()) {
    try {
      const stored =
        (await api.vip.readVIP({ UUID: change.vip.UUID }, silentCall)).vip ?? change.vip;
      const others = stored.virtualServersUuid.filter((uuid) => uuid !== vs.UUID);
      const virtualServersUuid = change.attach ? [...others, vs.UUID] : others;
      await api.vip.updateVIP(
        { UUID: change.vip.UUID, vip: { ...stored, virtualServersUuid } },
        silentCall,
      );
    } catch (err) {
      outcome.error = ConnectError.from(err).rawMessage;
      outcome.notDone = changes.slice(index).map((left) => changeStep(vs, left));
      return outcome;
    }
    outcome.done.push(changeStep(vs, change));
  }
  return outcome;
}

// VipAttach lets the user pick the VIPs that serve a virtual server, from the menu of its row or right after its Create
@Injectable({ providedIn: 'root' })
export class VipAttach {
  private api = inject(Api);
  private access = inject(Access);
  private dialog = inject(MatDialog);

  // edit resolves true when at least one VIP changed, so the page lists its resources again
  async edit(vs: VirtualServer, creating = false): Promise<boolean> {
    const vips = await listAll((limit, offset) =>
      this.api.vip.listVIP({ limit, offset }).then((resp) => resp.vip),
    ).catch(() => null);
    // the error dialog of the api already tells why the list failed
    if (vips === null) {
      return false;
    }
    const data: VipAttachData = { vs, vips, creating };
    const closed = this.dialog.open(VipAttachDialog, { data, width: '520px' }).afterClosed();
    const wanted: Set<string> | undefined = await firstValueFrom(closed);
    if (!wanted) {
      return false;
    }
    const changes = vipChanges(vs, vips, wanted);
    const refuse = (title: string, message: string) =>
      this.dialog.open(ErrorDialog, { data: { title, message } });

    // UpdateVIP needs the writer role on each VIP, checked before any change so none is half done for a role
    const cache = new Map();
    for (const change of changes) {
      const denial = await this.access.check(change.vip, Role.WRITER, cache);
      if (denial) {
        refuse(`Can't change the VIP ${change.vip.name}`, denial.reason);
        return false;
      }
    }
    // checking the boxes of a proposal already says what happens, a later change may also detach
    if (!creating) {
      const agreed = await confirm(this.dialog, {
        title: `Change the VIPs of ${vs.name}?`,
        message: 'In this order:',
        items: changes.map((change) => changeStep(vs, change)),
        confirm: 'Save',
      });
      if (!agreed) {
        return false;
      }
    }
    const outcome = await applyVipChanges(this.api, vs, changes);
    if (outcome.error) {
      const done = outcome.done.length > 0 ? ` Done: ${outcome.done.join(', ')}.` : '';
      refuse(
        `Changing the VIPs of ${vs.name} stopped`,
        `${outcome.error}. Not done: ${outcome.notDone.join(', ')}.${done}`,
      );
    }
    return outcome.done.length > 0;
  }
}
