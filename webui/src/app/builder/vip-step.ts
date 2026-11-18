import { Component, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectChange, MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { BuilderState, createChoice, newVipChoice, steps, vipWayOf } from './builder-state';
import { NewVipInput } from './documents';
import { ProposedName } from '../ui/proposed-name';

// VipStep picks the VIP the virtual server of the run listens on. It is an existing VIP the user can edit or a new one,
// created the same ways as on the VIPs page.
@Component({
  selector: 'flock-vip-step',
  imports: [
    ProposedName,
    FormsModule,
    MatButtonModule,
    MatButtonToggleModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatTooltipModule,
  ],
  template: `
    <mat-form-field>
      <mat-label>VIP</mat-label>
      <mat-select
        name="vip"
        [value]="state.vipPick()"
        (selectionChange)="state.pickVip($event.value)"
      >
        <mat-option value="">No VIP</mat-option>
        @for (vip of state.selectableVips(); track vip.UUID) {
          <mat-option [value]="vip.UUID">{{ vip.name }} ({{ vip.ip }})</mat-option>
        }
        <mat-option [value]="newVipChoice" [disabled]="state.newVipGreyed() !== ''">
          New VIP
          @if (state.newVipGreyed(); as reason) {
            <span class="reason">{{ reason }}</span>
          }
        </mat-option>
      </mat-select>
    </mat-form-field>

    @if (state.vipDenial(); as denial) {
      <p class="problem" role="alert">
        <mat-icon>block</mat-icon>
        {{ denial }}
      </p>
    }
    @if (state.pickedVip(); as vip) {
      <p class="note">The virtual server of the run is added to the VIP {{ vip.name }}.</p>
      @if (state.vipNameCount() > 1) {
        <p class="note">
          {{ state.vipNameCount() }} VIPs are named {{ vip.name }}, flock apply refuses the vip
          document of a generated file until you add the UUID of the one to use.
        </p>
      }
    }

    @if (state.vipPick() === newVipChoice) {
      <mat-form-field>
        <mat-label>Name</mat-label>
        <input
          matInput
          name="vipName"
          flockProposedName
          autocomplete="off"
          [ngModel]="vip().name"
          (ngModelChange)="update({ name: $event })"
        />
        @if (state.problemOf('vip.name'); as problem) {
          <mat-hint class="problem">{{ problem }}</mat-hint>
        }
      </mat-form-field>

      <mat-button-toggle-group
        name="vipWay"
        aria-label="IP of the VIP"
        [value]="vip().way"
        (change)="update({ way: $event.value })"
      >
        @for (way of state.vipWays(); track way.label) {
          <mat-button-toggle
            disabledInteractive
            [value]="wayOf(way)"
            [disabled]="way.greyed !== ''"
            [matTooltip]="way.greyed"
          >
            {{ way.label }}
          </mat-button-toggle>
        }
      </mat-button-toggle-group>

      @if (vip().way === 'explicit') {
        <mat-form-field>
          <mat-label>IP</mat-label>
          <input
            matInput
            name="vipIp"
            autocomplete="off"
            [ngModel]="vip().ip"
            (ngModelChange)="update({ ip: $event })"
          />
          @if (state.problemOf('vip.ip'); as problem) {
            <mat-hint class="problem">{{ problem }}</mat-hint>
          }
        </mat-form-field>
      } @else {
        <mat-button-toggle-group
          name="vipTarget"
          aria-label="Where the ip is acquired"
          [value]="vip().target"
          (change)="update({ target: $event.value })"
        >
          <mat-button-toggle value="instance">In the zone of an instance</mat-button-toggle>
          <mat-button-toggle value="zone">In a zone</mat-button-toggle>
        </mat-button-toggle-group>
        @if (vip().target === 'instance') {
          <mat-form-field>
            <mat-label>Instance</mat-label>
            <mat-select
              name="vipInstance"
              [value]="vip().instanceUuid"
              (selectionChange)="pickInstance($event)"
            >
              <mat-option
                [value]="createChoice"
                [disabled]="!!state.createGreyed('instance')"
                [matTooltip]="state.createGreyed('instance')"
              >
                <mat-icon>add</mat-icon>
                Create an instance
              </mat-option>
              @for (instance of state.instances(); track instance.UUID) {
                <mat-option [value]="instance.UUID">{{ instance.name }}</mat-option>
              }
            </mat-select>
            @if (state.problemOf('vip.instanceUuid'); as problem) {
              <mat-hint class="problem">{{ problem }}</mat-hint>
            } @else {
              <mat-hint>The VIP is added to the instance</mat-hint>
            }
          </mat-form-field>
        } @else {
          <mat-form-field>
            <mat-label>Zone</mat-label>
            <mat-select
              name="vipZone"
              [value]="vip().zoneUuid"
              (selectionChange)="pickZone($event)"
            >
              <mat-option
                [value]="createChoice"
                [disabled]="!!state.createGreyed('zone')"
                [matTooltip]="state.createGreyed('zone')"
              >
                <mat-icon>add</mat-icon>
                Create a zone
              </mat-option>
              @for (zone of state.zones(); track zone.UUID) {
                <mat-option [value]="zone.UUID">{{ zone.name }}</mat-option>
              }
            </mat-select>
            @if (state.problemOf('vip.zoneUuid'); as problem) {
              <mat-hint class="problem">{{ problem }}</mat-hint>
            } @else {
              <mat-hint>The VIP is added to no instance</mat-hint>
            }
          </mat-form-field>
        }
        <mat-form-field>
          <mat-label>Pool</mat-label>
          <input
            matInput
            name="vipPool"
            autocomplete="off"
            [ngModel]="vip().pool"
            (ngModelChange)="update({ pool: $event })"
          />
          <mat-hint>Empty for the default pool of the zone</mat-hint>
        </mat-form-field>
      }
    }

    <div class="actions">
      <span class="spacer"></span>
      <button
        mat-flat-button
        type="button"
        [disabled]="!state.vipDone()"
        (click)="state.step.set(steps.vs)"
      >
        Next
      </button>
    </div>
  `,
  styleUrl: './step.scss',
  styles: `
    .reason {
      display: block;
      font-size: 12px;
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class VipStep {
  protected state = inject(BuilderState);
  protected readonly steps = steps;
  protected readonly newVipChoice = newVipChoice;
  protected readonly createChoice = createChoice;
  protected vip = this.state.newVip;
  protected readonly wayOf = vipWayOf;

  protected update(change: Partial<NewVipInput>) {
    this.state.newVip.update((vip) => ({ ...vip, ...change }));
  }

  // the create entry is not a value, the select goes back to what it showed
  protected pickInstance(change: MatSelectChange) {
    if (change.value !== createChoice) {
      this.update({ instanceUuid: change.value });
      return;
    }
    change.source.value = this.vip().instanceUuid;
    this.state.createInstance();
  }

  protected pickZone(change: MatSelectChange) {
    if (change.value !== createChoice) {
      this.update({ zoneUuid: change.value });
      return;
    }
    change.source.value = this.vip().zoneUuid;
    this.state.createZone();
  }
}
