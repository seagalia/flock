import { NgTemplateOutlet } from '@angular/common';
import { Component, computed, input, model, output, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatSelectModule } from '@angular/material/select';
import {
  clone,
  create,
  DescField,
  DescMessage,
  DescOneof,
  equals,
  getOption,
  hasOption,
  Message,
} from '@bufbuild/protobuf';
import { scalarZeroValue } from '@bufbuild/protobuf/reflect';
import { oneof as oneofRules } from '../../gen/buf/validate/validate_pb';
import { Route, VirtualHost } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { Any, isWrapperDesc } from '@bufbuild/protobuf/wkt';
import { Choice, ChoiceCards } from '../ui/choice-cards';
import { AnyField } from './any-field';
import { ExternalAuthPicker, routeChoiceOf, routeExternalAuthOf } from './external-auth-picker';
import { FieldInput, fieldDoc, fieldLabel, messageLabel } from './field-input';
import { ListField } from './list-field';
import { OneofValue, SecretKeyField, secretOneof } from './secret-key-field';
import { FieldError } from './rules';
import { uuidKindOf, UuidPicker } from './uuid-picker';

export type { FieldError } from './rules';

const virtualHostExternalAuth = 'flock.api.config.route.v1.VirtualHost.external_auth';
const routeExternalAuth = 'flock.api.config.route.v1.Route.external_auth';
const virtualHostType = 'flock.api.config.route.v1.VirtualHost';

// The extension kinds the agents serving a resource support, 'any' when no agent serves it yet. A kind outside
// the list is offered disabled with the reason.
export interface AgentKinds {
  kinds: string[] | 'any';
  reason: string;
}

// How a field is edited, from one input to an Any extension (a list also edits a message nested deeper than the
// inline group). An empty message, a oneof case such as NoHealthCheck, has nothing to edit.
type Render = 'input' | 'uuid' | 'group' | 'list' | 'any' | 'externalAuth' | 'none';

// A oneof shows its cases as cards (choices), or as a select (choices null) when every case is a plain value. A
// oneof of a text and a reference to a secret value is one field with a Text / Secret toggle. The external auth oneof
// of a route is one select over the policies of the virtual server.
type Row =
  | { kind: 'oneof'; name: string; oneof: DescOneof; choices: Choice[] | null }
  | { kind: 'secret'; name: string; oneof: DescOneof }
  | { kind: 'externalAuth'; name: string; oneof: DescOneof }
  | { kind: 'field'; name: string; field: DescField };

// firstSentence is the line of a card, the start of the proto comment of its field
function firstSentence(doc: string): string {
  const sentence = doc.split(/\.(\s|$)/)[0];
  return sentence.replace(/\n/g, ' ').trim();
}

// choicesOf gives the cards of a oneof, null for one of plain values such as prefix or path, where cards would only
// repeat the names the select already shows
function choicesOf(oneof: DescOneof): Choice[] | null {
  const hasMessageCase = oneof.fields.some((field) => field.fieldKind === 'message');
  if (!hasMessageCase) {
    return null;
  }
  const cases = oneof.fields.map((field) => {
    const title = fieldLabel(field);
    const line = firstSentence(fieldDoc(field));
    // a doc that only names the case again would print the title twice
    const repeatsTitle = line.toLowerCase() === title.toLowerCase();
    return { value: field.localName, title, line: repeatsTitle ? '' : line };
  });
  const isRequired = hasOption(oneof, oneofRules) && getOption(oneof, oneofRules).required;
  if (isRequired) {
    return cases;
  }
  return [{ value: '', title: 'None', line: 'Leave it unset' }, ...cases];
}

// Messages held as one value by protobuf-es outside a oneof, so one input edits them
function isOneValue(field: DescField & { fieldKind: 'message' }): boolean {
  const typeName = field.message.typeName;
  if (typeName === 'google.protobuf.Duration' || typeName === 'google.protobuf.Timestamp') {
    return true;
  }
  const unboxed = typeName === 'google.protobuf.Struct' || isWrapperDesc(field.message);
  return unboxed && !field.oneof;
}

function renderOf(field: DescField, depth: number): Render {
  if (`${field.parent.typeName}.${field.name}` === virtualHostExternalAuth) {
    return 'externalAuth';
  }
  if (field.fieldKind === 'list' || field.fieldKind === 'map') {
    return 'list';
  }
  if (field.fieldKind === 'message') {
    if (field.message.typeName === 'google.protobuf.Any') {
      return 'any';
    }
    if (isOneValue(field)) {
      return 'input';
    }
    if (field.message.fields.length === 0) {
      return 'none';
    }
    // one level of inline group keeps the form readable, deeper messages open in a stacked panel, except the case of a
    // card the user just picked, whose fields they fill next
    const isOneofCase = field.oneof !== undefined;
    return depth === 0 || isOneofCase ? 'group' : 'list';
  }
  return uuidKindOf(field) ? 'uuid' : 'input';
}

// The members a message shows first, by message type. The proto order puts a choice deciding what the other fields
// mean after them, the form asks it first.
const leadingMembers: Record<string, string[]> = {
  'flock.api.config.core.v1.HealthCheck': ['health_checker'],
};

// The fields a message folds under Advanced settings, by message type: most users never change them, they keep the
// form short.
const advancedMembers: Record<string, string[]> = {
  'flock.api.config.backend.v1.Backend': ['connect_timeout', 'refresh_rate'],
  'flock.api.config.core.v1.HealthCheck': [
    'unhealthy_threshold',
    'healthy_threshold',
    'unhealthy_interval',
    'alt_port',
    'enable_tls',
  ],
  'flock.api.config.core.v1.HttpHealthCheck': [
    'send',
    'receive',
    'request_headers_to_add',
    'expected_statuses',
    'codec_client_type',
    'method',
  ],
};

// ProtoForm renders and edits any message from its descriptor. It works on copies, every change sets value to a
// new message built from a clone of the current one, so the fields it doesn't show are kept as they are.
@Component({
  selector: 'flock-proto-form',
  imports: [
    AnyField,
    ChoiceCards,
    ExternalAuthPicker,
    FieldInput,
    ListField,
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatSelectModule,
    NgTemplateOutlet,
    SecretKeyField,
    UuidPicker,
  ],
  template: `
    @for (row of mainRows(); track row.name) {
      <ng-container
        [ngTemplateOutlet]="rowTemplate"
        [ngTemplateOutletContext]="{ $implicit: row }"
      />
    }
    @if (advancedRows().length > 0) {
      <button
        mat-button
        type="button"
        class="advanced-toggle"
        [attr.aria-expanded]="advancedShown()"
        [disabled]="advancedHeld()"
        (click)="advancedOpen.set(!advancedOpen())"
      >
        <mat-icon>{{ advancedShown() ? 'expand_less' : 'expand_more' }}</mat-icon>
        Advanced settings
      </button>
      @if (advancedShown()) {
        <div class="advanced">
          @for (row of advancedRows(); track row.name) {
            <ng-container
              [ngTemplateOutlet]="rowTemplate"
              [ngTemplateOutletContext]="{ $implicit: row }"
            />
          }
        </div>
      }
    }

    <ng-template #rowTemplate let-row>
      <div class="row" [attr.data-field]="row.name">
        @if (row.kind === 'secret') {
          <flock-secret-key-field
            [oneof]="row.oneof"
            [value]="oneofValue(row.oneof)"
            [disabled]="isRowReadonly(row)"
            (valueChange)="writeOneof(row.oneof, $event)"
          />
          @for (message of messagesAt(row.name); track $index) {
            <div class="field-error">{{ message }}</div>
          }
        } @else if (row.kind === 'externalAuth') {
          <flock-external-auth-picker
            mode="route"
            [names]="shownPolicyNames()"
            [value]="routeChoice()"
            [disabled]="isRowReadonly(row)"
            [help]="oneofHelp(row.oneof)"
            (valueChange)="writeRouteExternalAuth(row.oneof, $event)"
          />
          @for (message of messagesAt(row.name); track $index) {
            <div class="field-error">{{ message }}</div>
          }
        } @else if (row.kind === 'oneof') {
          @if (row.choices) {
            <div class="oneof">
              <span class="oneof-label">{{ oneofLabel(row.oneof) }}</span>
              <flock-choice-cards
                [attr.data-oneof]="row.oneof.name"
                [label]="oneofLabel(row.oneof)"
                [choices]="row.choices"
                [value]="selectedCase(row.oneof)"
                [disabled]="isRowReadonly(row)"
                (picked)="selectCase(row.oneof, $event)"
              />
            </div>
          } @else {
            <mat-form-field>
              <mat-label>{{ oneofLabel(row.oneof) }}</mat-label>
              <mat-select
                [attr.name]="row.oneof.name"
                [value]="selectedCase(row.oneof)"
                [disabled]="isRowReadonly(row)"
                (selectionChange)="selectCase(row.oneof, $event.value)"
              >
                <mat-option value="">None</mat-option>
                @for (field of row.oneof.fields; track field.number) {
                  <mat-option [value]="field.localName">{{ label(field) }}</mat-option>
                }
              </mat-select>
            </mat-form-field>
          }
          @for (message of messagesAt(row.name); track $index) {
            <div class="field-error">{{ message }}</div>
          }
          @if (selectedField(row.oneof); as field) {
            <div class="case" [attr.data-field]="field.name">
              <ng-container
                [ngTemplateOutlet]="fieldTemplate"
                [ngTemplateOutletContext]="{ $implicit: field, locked: isRowReadonly(row) }"
              />
            </div>
          }
        } @else {
          <ng-container
            [ngTemplateOutlet]="fieldTemplate"
            [ngTemplateOutletContext]="{ $implicit: row.field, locked: isRowReadonly(row) }"
          />
        }
      </div>
    </ng-template>

    <ng-template #fieldTemplate let-field let-locked="locked">
      @switch (render(field)) {
        @case ('input') {
          <flock-field-input
            [field]="field"
            [value]="read(field)"
            [disabled]="locked"
            [errors]="messagesAt(field.name)"
            (valueChange)="write(field, $event)"
          />
        }
        @case ('uuid') {
          <flock-uuid-picker
            [kind]="uuidKind(field)"
            [label]="label(field)"
            [name]="field.name"
            [help]="help(field)"
            [disabled]="locked"
            [value]="readText(field)"
            (valueChange)="write(field, $event)"
          />
          @for (message of messagesAt(field.name); track $index) {
            <div class="field-error">{{ message }}</div>
          }
        }
        @case ('externalAuth') {
          <flock-external-auth-picker
            mode="vhost"
            [names]="shownPolicyNames()"
            [value]="readText(field)"
            [disabled]="locked"
            [help]="help(field)"
            (valueChange)="write(field, $event)"
          />
          @for (message of messagesAt(field.name); track $index) {
            <div class="field-error">{{ message }}</div>
          }
        }
        @case ('group') {
          <fieldset class="group">
            <legend>{{ label(field) }}</legend>
            @for (message of messagesAt(field.name); track $index) {
              <div class="field-error">{{ message }}</div>
            }
            <flock-proto-form
              [schema]="field.message"
              [value]="messageOf(field)"
              [allReadonly]="locked"
              [errors]="errorsUnder(field.name)"
              [agentKinds]="agentKinds()"
              [policyNames]="policyNames()"
              [depth]="depth() + 1"
              [owner]="ownerLabel()"
              (valueChange)="write(field, $event)"
            />
          </fieldset>
        }
        @case ('list') {
          <flock-list-field
            [field]="field"
            [value]="read(field)"
            [disabled]="locked"
            [errors]="listErrors(field.name)"
            [agentKinds]="agentKinds()"
            [policyNames]="policyNames()"
            [hostPolicy]="hostPolicy()"
            [owner]="ownerLabel()"
            [itemGreyed]="itemGreyed()[field.name] ?? noItemGreyed"
            (valueChange)="write(field, $event)"
          />
        }
        @case ('any') {
          <flock-any-field
            [field]="field"
            [value]="readAny(field)"
            [disabled]="locked"
            [errors]="errorsUnder(field.name)"
            [agentKinds]="agentKinds()"
            [policyNames]="policyNames()"
            [depth]="depth() + 1"
            [owner]="ownerLabel()"
            (valueChange)="write(field, $event)"
          />
          @for (message of messagesAt(field.name); track $index) {
            <div class="field-error">{{ message }}</div>
          }
        }
      }
    </ng-template>
  `,
  styles: `
    :host {
      display: block;
    }
    mat-form-field {
      width: 100%;
    }
    .group {
      border: 1px solid var(--flock-border);
      border-radius: 8px;
      padding: 8px 16px;
      margin: 0 0 16px;
    }
    legend {
      padding: 0 4px;
      font-weight: 500;
    }
    .oneof {
      display: flex;
      flex-direction: column;
      gap: 8px;
      margin: 0 0 16px;
    }
    .oneof-label {
      font-weight: 500;
    }
    .advanced-toggle {
      margin: 0 0 12px -12px;
    }
    .field-error {
      color: var(--mat-sys-error);
      font-size: 0.85em;
      margin: -12px 0 12px 16px;
    }
  `,
})
export class ProtoForm {
  readonly schema = input.required<DescMessage>();
  readonly value = model.required<Message>();
  // proto field (or oneof) names shown, in that order, null shows every field
  readonly fields = input<string[] | null>(null);
  readonly readonly = input<string[]>([]);
  readonly errors = input<FieldError[]>([]);
  readonly agentKinds = input<AgentKinds | null>(null);
  // the external auth policies of the virtual server being edited, null outside a virtual server
  readonly policyNames = input<string[] | null>(null);
  // why an item of a list field can't be changed, by field name of this message (pools), see ListField
  readonly itemGreyed = input<Record<string, (item: Message) => string>>({});
  readonly dirty = output<boolean>();

  // The components nesting a form set these. Depth limits the inline groups to one level, and owner keeps the
  // Apply buttons of nested lists naming the message the panel edits. allReadonly passes a read-only field down to
  // every form under it.
  readonly depth = input(0);
  readonly owner = input('');
  readonly allReadonly = input(false);

  protected readonly noItemGreyed = () => '';

  protected readonly shownPolicyNames = computed(() => this.policyNames() ?? []);
  // the default policy of a virtual host, which the rows of its routes show as inherited
  protected readonly hostPolicy = computed(() => {
    const isVirtualHost = this.schema().typeName === virtualHostType;
    return isVirtualHost ? (this.value() as VirtualHost).externalAuth : '';
  });

  protected readonly ownerLabel = computed(() => this.owner() || messageLabel(this.schema()));

  protected readonly rows = computed<Row[]>(() => {
    const wanted = this.fields();
    const members = this.schema().members.filter(
      (member) => member.kind === 'oneof' || !member.oneof,
    );
    const leading = (leadingMembers[this.schema().typeName] ?? []).map((name) =>
      this.memberNamed(name),
    );
    const shown =
      wanted === null ? [...leading, ...members] : wanted.map((name) => this.memberNamed(name));
    const rows: Row[] = [];
    for (const member of shown) {
      if (!member) {
        continue;
      }
      const row = this.rowOf(member);
      if (!rows.some((existing) => existing.name === row.name)) {
        rows.push(row);
      }
    }
    return rows;
  });

  private readonly advancedNames = computed(() => advancedMembers[this.schema().typeName] ?? []);
  protected readonly mainRows = computed(() =>
    this.rows().filter((row) => !this.advancedNames().includes(row.name)),
  );
  protected readonly advancedRows = computed(() =>
    this.rows().filter((row) => this.advancedNames().includes(row.name)),
  );
  protected readonly advancedOpen = signal(false);
  // an advanced field with an error stays in view, folding it would hide what to fix. A set one doesn't: the
  // controller fills the defaults, so every stored resource has them set.
  protected readonly advancedHeld = computed(() =>
    this.advancedRows().some((row) =>
      this.errors().some(
        (error) => error.path === row.name || error.path.startsWith(row.name + '.'),
      ),
    ),
  );
  protected readonly advancedShown = computed(() => this.advancedOpen() || this.advancedHeld());

  private readonly renders = computed(() => {
    const depth = this.depth();
    return new Map(this.schema().fields.map((field) => [field, renderOf(field, depth)]));
  });

  // what an unset message shows in its group, the same object on every render until the user edits it
  private defaults = new Map<DescField, Message>();
  // what this form emitted last and the value it started from, to tell its own edits from a new input value
  private emitted: Message | undefined;
  private baseline: Message | undefined;

  protected render(field: DescField): Render {
    return this.renders().get(field) ?? 'input';
  }

  protected label(field: DescField): string {
    return fieldLabel(field);
  }

  protected help(field: DescField): string {
    return fieldDoc(field);
  }

  protected oneofLabel(oneof: DescOneof): string {
    const words = oneof.name.replaceAll('_', ' ');
    return words.charAt(0).toUpperCase() + words.slice(1);
  }

  protected uuidKind(field: DescField) {
    return uuidKindOf(field)!;
  }

  // A oneof is one choice, so naming any of its fields read-only locks the case select along with the case, the
  // same way naming one of them in fields shows the whole oneof.
  protected isRowReadonly(row: Row): boolean {
    const readonly = this.readonly();
    if (this.allReadonly() || readonly.includes(row.name)) {
      return true;
    }
    const isOneof = row.kind !== 'field';
    return isOneof && row.oneof.fields.some((field) => readonly.includes(field.name));
  }

  protected messagesAt(path: string): string[] {
    return this.errors()
      .filter((error) => error.path === path)
      .map((error) => error.message);
  }

  // the errors inside a message field, their paths relative to it
  protected errorsUnder(name: string): FieldError[] {
    return this.errors()
      .filter((error) => error.path.startsWith(name + '.'))
      .map((error) => ({ ...error, path: error.path.slice(name.length + 1) }));
  }

  // the errors of a list or a map, such as [0].backend_uuid, ["key"] or the empty path for the field itself
  protected listErrors(name: string): FieldError[] {
    return this.errors()
      .filter((error) => {
        const rest = error.path.slice(name.length);
        const underField = rest === '' || rest.startsWith('[') || rest.startsWith('.');
        return error.path.startsWith(name) && underField;
      })
      .map((error) => ({ ...error, path: error.path.slice(name.length) }));
  }

  protected read(field: DescField): unknown {
    const fields = this.value() as unknown as Record<string, unknown>;
    if (!field.oneof) {
      return fields[field.localName];
    }
    const selected = fields[field.oneof.localName] as { case: string | undefined; value: unknown };
    return selected.case === field.localName ? selected.value : undefined;
  }

  protected readText(field: DescField): string {
    return (this.read(field) as string | undefined) ?? '';
  }

  protected readAny(field: DescField): Any | undefined {
    return this.read(field) as Any | undefined;
  }

  protected messageOf(field: DescField): Message {
    const stored = this.read(field) as Message | undefined;
    if (stored) {
      return stored;
    }
    if (field.fieldKind !== 'message') {
      throw new Error(`${field.name} is not a message`);
    }
    let fallback = this.defaults.get(field);
    if (!fallback) {
      fallback = create(field.message);
      this.defaults.set(field, fallback);
    }
    return fallback;
  }

  private rowOf(member: DescField | DescOneof): Row {
    if (member.kind === 'field') {
      return { kind: 'field', name: member.name, field: member };
    }
    if (`${member.parent.typeName}.${member.name}` === routeExternalAuth) {
      return { kind: 'externalAuth', name: member.name, oneof: member };
    }
    if (secretOneof(member)) {
      return { kind: 'secret', name: member.name, oneof: member };
    }
    return { kind: 'oneof', name: member.name, oneof: member, choices: choicesOf(member) };
  }

  protected oneofValue(oneof: DescOneof): OneofValue {
    const fields = this.value() as unknown as Record<string, unknown>;
    return fields[oneof.localName] as OneofValue;
  }

  protected routeChoice(): string {
    return routeChoiceOf((this.value() as Route).externalAuth);
  }

  protected writeRouteExternalAuth(oneof: DescOneof, choice: string) {
    this.writeOneof(oneof, routeExternalAuthOf(choice));
  }

  // the docs hold no comment of a oneof itself, its cases tell what the choices do
  protected oneofHelp(oneof: DescOneof): string {
    return oneof.fields.map((field) => fieldDoc(field)).join(' ');
  }

  protected writeOneof(oneof: DescOneof, value: OneofValue) {
    const next = this.copy();
    const fields = next as unknown as Record<string, unknown>;
    fields[oneof.localName] = value;
    this.commit(next);
  }

  protected selectedCase(oneof: DescOneof): string {
    const fields = this.value() as unknown as Record<string, unknown>;
    return (fields[oneof.localName] as { case: string | undefined }).case ?? '';
  }

  protected selectedField(oneof: DescOneof): DescField | undefined {
    const selected = this.selectedCase(oneof);
    return oneof.fields.find((field) => field.localName === selected);
  }

  protected selectCase(oneof: DescOneof, localName: string) {
    // picking the case already set again would reset what it holds
    if (localName === this.selectedCase(oneof)) {
      return;
    }
    const field = oneof.fields.find((f) => f.localName === localName);
    const next = this.copy();
    const fields = next as unknown as Record<string, unknown>;
    fields[oneof.localName] = field
      ? { case: field.localName, value: zeroOf(field) }
      : { case: undefined };
    this.commit(next);
  }

  protected write(field: DescField, value: unknown) {
    const next = this.copy();
    const fields = next as unknown as Record<string, unknown>;
    if (field.oneof) {
      fields[field.oneof.localName] = { case: field.localName, value };
    } else {
      fields[field.localName] = value;
    }
    this.commit(next);
  }

  private memberNamed(name: string): DescField | DescOneof | undefined {
    const direct = this.schema().members.find((member) => member.name === name);
    // a field of a oneof named alone shows its whole oneof, the case select comes with it
    const inOneof = this.schema().fields.find((field) => field.name === name)?.oneof;
    return inOneof ?? direct;
  }

  private copy(): Message {
    return clone(this.schema(), this.value());
  }

  private commit(next: Message) {
    const current = this.value();
    // a value this form didn't emit is a new starting point (a reload, or the first edit)
    if (current !== this.emitted) {
      this.baseline = current;
    }
    this.emitted = next;
    this.value.set(next);
    this.dirty.emit(!equals(this.schema(), this.baseline!, next));
  }
}

function zeroOf(field: DescField): unknown {
  switch (field.fieldKind) {
    case 'message':
      return create(field.message);
    case 'enum':
      return field.enum.values[0]?.number ?? 0;
    case 'scalar':
      return scalarZeroValue(field.scalar, field.longAsString);
    default:
      return undefined;
  }
}
