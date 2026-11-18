import { Component, computed, forwardRef, input, model } from '@angular/core';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { create, DescField, DescMessage, Message } from '@bufbuild/protobuf';
import { Any, anyPack, anyUnpack } from '@bufbuild/protobuf/wkt';
import { extensionKinds } from '../core/proto-docs.gen';
import { registry } from '../core/registry';
import { typeUrlPrefix } from '../core/resources';
import { fieldDoc, fieldLabel } from './field-input';
import type { AgentKinds, FieldError } from './proto-form';
import { ProtoForm } from './proto-form';

const unsupportedNote = 'Not supported by the agents of this resource';

interface KindOption {
  name: string;
  label: string;
  // why the agents of the resource can't take this kind, empty when they can
  reason: string;
}

// kindLabel names an extension by its message and the agent its package is for, so
// flock.extensions.vs.starling_options.v1.HttpOptions reads HttpOptions (starling)
function kindLabel(name: string): string {
  const parts = name.split('.');
  const message = parts[parts.length - 1];
  const agent = parts.find((part) => part.includes('_'))?.split('_')[0];
  return agent ? `${message} (${agent})` : message;
}

// AnyField edits a google.protobuf.Any extension field with a select of the kinds its proto comment lists, then
// the form of the chosen message. The stored Any stays untouched until the user changes it, so a kind the dashboard
// doesn't know, or the agents don't support, is kept on save.
@Component({
  selector: 'flock-any-field',
  imports: [
    forwardRef(() => ProtoForm),
    MatFormFieldModule,
    MatIconModule,
    MatSelectModule,
    MatTooltipModule,
  ],
  template: `
    <mat-form-field>
      <mat-label>{{ label() }}</mat-label>
      <mat-select
        [attr.name]="field().name"
        [value]="storedName()"
        [disabled]="disabled()"
        (selectionChange)="pick($event.value)"
      >
        <mat-option value="">None</mat-option>
        @for (option of options(); track option.name) {
          <mat-option [value]="option.name" [disabled]="option.reason !== ''">
            {{ option.label }}
            @if (option.reason) {
              <span class="reason">{{ option.reason }}</span>
            }
          </mat-option>
        }
        @if (storedOutside()) {
          <mat-option [value]="storedName()">{{ storedLabel() }}</mat-option>
        }
      </mat-select>
      @if (help()) {
        <mat-icon matSuffix class="help" [matTooltip]="help()">info_outline</mat-icon>
      }
    </mat-form-field>
    @if (unsupported()) {
      <p class="flag">{{ unsupportedNote }}. It is kept as it is on save.</p>
    }
    @if (unpacked(); as message) {
      <flock-proto-form
        [schema]="message.schema"
        [value]="message.value"
        [allReadonly]="disabled()"
        [errors]="errors()"
        [agentKinds]="agentKinds()"
        [policyNames]="policyNames()"
        [depth]="depth()"
        [owner]="owner()"
        (valueChange)="edit(message.schema, $event)"
      />
    } @else if (storedName()) {
      <p class="flag">
        The dashboard doesn't know {{ storedName() }}, its content can't be edited here.
      </p>
    }
  `,
  styles: `
    :host {
      display: block;
    }
    mat-form-field {
      width: 100%;
    }
    .reason {
      margin-left: 8px;
      font-size: 0.85em;
    }
    .flag {
      color: var(--mat-sys-error);
      margin-top: -8px;
    }
    .help {
      font-size: 18px;
      width: 18px;
      height: 18px;
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class AnyField {
  readonly field = input.required<DescField>();
  readonly value = model<Any | undefined>();
  readonly agentKinds = input<AgentKinds | null>(null);
  readonly policyNames = input<string[] | null>(null);
  readonly disabled = input(false);
  readonly errors = input<FieldError[]>([]);
  readonly depth = input(0);
  readonly owner = input('');

  protected readonly unsupportedNote = unsupportedNote;
  protected readonly label = computed(() => fieldLabel(this.field()));
  protected readonly help = computed(() => fieldDoc(this.field()));

  private readonly candidates = computed(
    () => extensionKinds[`${this.field().parent.typeName}.${this.field().name}`] ?? [],
  );

  protected readonly options = computed<KindOption[]>(() =>
    this.candidates().map((name) => ({
      name,
      label: kindLabel(name),
      reason: this.refusal(name),
    })),
  );

  protected readonly storedName = computed(() => {
    const any = this.value();
    return any ? any.typeUrl.replace(typeUrlPrefix, '') : '';
  });

  // a stored kind the comment of the field doesn't list still needs its own option to be shown selected
  protected readonly storedOutside = computed(
    () => this.storedName() !== '' && !this.candidates().includes(this.storedName()),
  );

  protected readonly storedLabel = computed(
    () => `${kindLabel(this.storedName())}, ${unsupportedNote}`,
  );

  protected readonly unsupported = computed(() => {
    const stored = this.storedName();
    return stored !== '' && (this.storedOutside() || this.refusal(stored) !== '');
  });

  protected readonly unpacked = computed(() => {
    const any = this.value();
    const schema = any ? registry.getMessage(this.storedName()) : undefined;
    if (!any || !schema) {
      return undefined;
    }
    try {
      const value = anyUnpack(any, schema);
      return value ? { schema, value } : undefined;
    } catch {
      // bytes that don't decode as their kind can't be shown in a form, they stay stored as they are
      return undefined;
    }
  });

  protected pick(name: string) {
    if (name === this.storedName()) {
      return;
    }
    if (name === '') {
      this.value.set(undefined);
      return;
    }
    const schema = registry.getMessage(name);
    if (schema) {
      this.value.set(anyPack(schema, create(schema)));
    }
  }

  protected edit(schema: DescMessage, message: Message) {
    this.value.set(anyPack(schema, message));
  }

  private refusal(name: string): string {
    const agentKinds = this.agentKinds();
    // every kind is offered when there is no agent to ask (null) or none serves the resource yet ('any')
    if (agentKinds === null || agentKinds.kinds === 'any') {
      return '';
    }
    const supported = agentKinds.kinds.some((kind) => kind.replace(typeUrlPrefix, '') === name);
    if (supported) {
      return '';
    }
    // agentKinds only explains an empty shared list, a kind left out of a non-empty one still needs a reason
    return agentKinds.reason || 'Not every agent of this resource supports it.';
  }
}
