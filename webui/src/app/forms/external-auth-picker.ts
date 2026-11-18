import { Component, computed, input, output } from '@angular/core';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { DescMessage, Message } from '@bufbuild/protobuf';
import { Route } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import {
  VirtualServer,
  VirtualServerSchema,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';

// The choice of a route that turns the check off. A policy name matches ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$, so no
// policy can take this value.
export const disabledChoice = '(disabled)';

export type ExternalAuthMode = 'vhost' | 'route';

type RouteExternalAuth = Route['externalAuth'];

// policyNamesOf gives the policies a virtual server defines, null for a message of another kind, whose forms then
// offer no policy
export function policyNamesOf(schema: DescMessage, message: Message): string[] | null {
  if (schema.typeName !== VirtualServerSchema.typeName) {
    return null;
  }
  const routeConfig = (message as VirtualServer).routeConfig;
  if (routeConfig.case !== 'httpRoutesConfig') {
    return [];
  }
  return routeConfig.value.externalAuths.map((policy) => policy.name).filter((name) => name !== '');
}

export function routeChoiceOf(externalAuth: RouteExternalAuth): string {
  if (externalAuth.case === 'externalAuthName') {
    return externalAuth.value;
  }
  const isDisabled = externalAuth.case === 'externalAuthDisabled' && externalAuth.value;
  return isDisabled ? disabledChoice : '';
}

export function routeExternalAuthOf(choice: string): RouteExternalAuth {
  if (choice === '') {
    return { case: undefined };
  }
  if (choice === disabledChoice) {
    return { case: 'externalAuthDisabled', value: true };
  }
  return { case: 'externalAuthName', value: choice };
}

// effectivePolicy names the policy checking a route in its row, empty when none does
export function effectivePolicy(route: Route, hostPolicy: string): string {
  const choice = routeChoiceOf(route.externalAuth);
  if (choice === disabledChoice) {
    return 'disabled';
  }
  if (choice !== '') {
    return choice;
  }
  return hostPolicy === '' ? '' : `${hostPolicy} (inherited)`;
}

// ExternalAuthPicker picks the external auth policy of a virtual host or a route among the policies of the virtual
// server being edited. A route also inherits the policy of its virtual host, or turns the check off.
@Component({
  selector: 'flock-external-auth-picker',
  imports: [MatFormFieldModule, MatIconModule, MatSelectModule, MatTooltipModule],
  template: `
    <mat-form-field>
      <mat-label>External auth</mat-label>
      <mat-select
        name="external_auth"
        [value]="value()"
        [disabled]="disabled()"
        (selectionChange)="valueChange.emit($event.value)"
      >
        @if (mode() === 'vhost') {
          <mat-option value="">None</mat-option>
        } @else {
          <mat-option value="">Inherit</mat-option>
          <mat-option [value]="disabledChoice">Disabled</mat-option>
        }
        @for (name of names(); track name) {
          <mat-option [value]="name">{{ name }}</mat-option>
        }
        @if (isUnlisted()) {
          <mat-option [value]="value()">{{ value() }}</mat-option>
        }
      </mat-select>
      @if (help()) {
        <mat-icon matSuffix class="help" [matTooltip]="help()">info_outline</mat-icon>
      }
    </mat-form-field>
  `,
  styles: `
    :host {
      display: block;
    }
    mat-form-field {
      width: 100%;
    }
    .help {
      font-size: 18px;
      width: 18px;
      height: 18px;
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class ExternalAuthPicker {
  readonly names = input<string[]>([]);
  // a policy name, empty for none (vhost) or inherit (route), disabledChoice for a route turning the check off
  readonly value = input('');
  readonly mode = input<ExternalAuthMode>('vhost');
  readonly disabled = input(false);
  readonly help = input('');
  readonly valueChange = output<string>();

  protected readonly disabledChoice = disabledChoice;
  // a name no policy defines any more still shows, so the select doesn't look empty while the rules flag it
  protected readonly isUnlisted = computed(() => {
    const value = this.value();
    const isName = value !== '' && value !== disabledChoice;
    return isName && !this.names().includes(value);
  });
}
