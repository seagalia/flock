import {
  Component,
  computed,
  forwardRef,
  inject,
  input,
  linkedSignal,
  output,
  signal,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatChipInputEvent, MatChipsModule } from '@angular/material/chips';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import {
  clone,
  create,
  DescField,
  DescMessage,
  equals,
  Message,
  ScalarType,
} from '@bufbuild/protobuf';
import { scalarZeroValue } from '@bufbuild/protobuf/reflect';
import { EmptySchema } from '@bufbuild/protobuf/wkt';
import { confirm, ConfirmData } from '../ui/confirm-dialog';
import { SidePanel } from '../ui/side-panel';
import { parseSecretRef } from '../core/secret-refs';
import { Route, RouteSchema } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { effectivePolicy } from './external-auth-picker';
import { fieldDoc, fieldLabel, formatScalar, parseScalar } from './field-input';
import type { AgentKinds, FieldError } from './proto-form';
import { ProtoForm } from './proto-form';
import { SecretRefLink, SecretRefPicker } from './secret-ref-picker';
import { UuidPicker, uuidKindOf } from './uuid-picker';

// The item open in the stacked panel, with its position (null for a new item) and the item as it was when opened
interface Editing {
  index: number | null;
  original: Message;
}

// whether the value of a metadata entry is typed, or points at a secret
type ValueKind = 'text' | 'secret';

type Mode = 'messages' | 'single' | 'chips' | 'enum-chips' | 'uuids' | 'map' | 'unsupported';

// The map of values of a secret, which reads give with empty values. A key it held when the form opened keeps its
// stored value while its input stays empty.
const secretDataField = 'flock.api.config.secret.v1.Secret.data';

// The kinds whose own metadata can point at a secret. A secret's metadata can't, and the metadata nested deeper (zone
// pools, virtual hosts) can't either, because the controller refuses references there.
const metadataWithSecretRefs = new Set([
  'flock.api.config.vs.v1.VirtualServer',
  'flock.api.config.backend.v1.Backend',
  'flock.api.config.vip.v1.VIP',
  'flock.api.config.cert.v1.Certificate',
  'flock.api.config.zone.v1.Zone',
  'flock.api.config.instance.v1.Instance',
]);

// One-click values of some chip lists, by full field name. The value replaces the whole list, since it already
// covers what the list holds. Its chip reads the chip text instead of the value, the name the config map gives it.
const shortcuts: Record<string, { label: string; value: string; chip: string }> = {
  'flock.api.config.route.v1.VirtualHost.domains': {
    label: 'Any domain',
    value: '*',
    chip: 'any domain',
  },
};

// The header names of an external auth policy, which the controller lowercases before checking its rules. The chips
// lowercase them as they are added, so the rules checked as you type see what the controller checks (Host refused).
const lowercasedFields = new Set([
  'flock.api.config.route.v1.ExternalAuth.request_headers',
  'flock.api.config.route.v1.ExternalAuth.upstream_headers',
  'flock.api.config.route.v1.ExternalAuth.client_headers',
]);

// ListField edits the fields one input can't hold, such as repeated fields and maps, and the messages nested deeper
// than the inline group of the form. A message item opens in a stacked side panel with its own form, so the parent
// form stays readable however deep the message goes, and Apply to <owner> hands the item back here.
@Component({
  selector: 'flock-list-field',
  imports: [
    forwardRef(() => ProtoForm),
    MatButtonModule,
    MatButtonToggleModule,
    MatChipsModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatTooltipModule,
    SecretRefLink,
    SecretRefPicker,
    SidePanel,
    UuidPicker,
  ],
  template: `
    <div class="head">
      <span class="label">{{ label() }}</span>
      @if (help()) {
        <mat-icon class="help" [matTooltip]="help()">info_outline</mat-icon>
      }
    </div>
    @switch (mode()) {
      @case ('messages') {
        @for (item of items(); track $index) {
          @let greyed = itemGreyed()(asMessage(item));
          <div class="item" [class.invalid]="itemHasErrors($index)">
            <button
              mat-button
              type="button"
              class="open"
              [matTooltip]="greyed"
              (click)="open($index)"
            >
              @if (greyed) {
                <mat-icon>lock</mat-icon>
              }
              {{ summary(asMessage(item), $index) }}
            </button>
            @if (!disabled()) {
              <button
                mat-icon-button
                type="button"
                aria-label="Remove"
                [disabled]="!!greyed"
                disabledInteractive
                [matTooltip]="greyed"
                (click)="remove($index)"
              >
                <mat-icon>delete</mat-icon>
              </button>
            }
          </div>
        } @empty {
          <p class="empty">None</p>
        }
        @if (!disabled()) {
          <button mat-stroked-button type="button" (click)="open(null)">Add</button>
        }
      }
      @case ('single') {
        @if (value() !== undefined) {
          <div class="item" [class.invalid]="errors().length > 0">
            <button mat-button type="button" class="open" (click)="open(0)">
              {{ summary(asMessage(value()), 0) }}
            </button>
            @if (!disabled()) {
              <button mat-icon-button type="button" aria-label="Remove" (click)="clear()">
                <mat-icon>delete</mat-icon>
              </button>
            }
          </div>
        } @else if (!disabled()) {
          <button mat-stroked-button type="button" (click)="open(null)">Set</button>
        } @else {
          <p class="empty">None</p>
        }
      }
      @case ('chips') {
        <mat-form-field>
          <mat-chip-grid #grid [disabled]="disabled()">
            @for (item of items(); track $index) {
              <mat-chip-row (removed)="remove($index)">
                {{ chipLabel(item) }}
                <button matChipRemove [attr.aria-label]="'Remove ' + format(item)">
                  <mat-icon>cancel</mat-icon>
                </button>
              </mat-chip-row>
            }
            <input
              [attr.name]="field().name"
              placeholder="Type then Enter"
              [matChipInputFor]="grid"
              (matChipInputTokenEnd)="addChip($event)"
            />
          </mat-chip-grid>
        </mat-form-field>
        @if (shortcut(); as shortcut) {
          <button
            mat-stroked-button
            type="button"
            class="shortcut"
            [disabled]="disabled() || holdsOnly(shortcut.value)"
            (click)="valueChange.emit([shortcut.value])"
          >
            {{ shortcut.label }} ({{ shortcut.value }})
          </button>
        }
      }
      @case ('enum-chips') {
        <mat-chip-set>
          @for (item of items(); track $index) {
            <mat-chip (removed)="remove($index)" [removable]="!disabled()">
              {{ enumName(item) }}
              @if (!disabled()) {
                <button matChipRemove [attr.aria-label]="'Remove ' + enumName(item)">
                  <mat-icon>cancel</mat-icon>
                </button>
              }
            </mat-chip>
          }
        </mat-chip-set>
        @if (!disabled()) {
          <mat-form-field>
            <mat-label>Add</mat-label>
            <mat-select [value]="null" (selectionChange)="add($event.value)">
              @for (option of enumValues(); track option.number) {
                <mat-option [value]="option.number">{{ option.name }}</mat-option>
              }
            </mat-select>
          </mat-form-field>
        }
      }
      @case ('uuids') {
        <mat-chip-set>
          @for (item of items(); track $index) {
            <mat-chip (removed)="remove($index)" [removable]="!disabled()">
              {{ item }}
              @if (!disabled()) {
                <button matChipRemove [attr.aria-label]="'Remove ' + item">
                  <mat-icon>cancel</mat-icon>
                </button>
              }
            </mat-chip>
          }
        </mat-chip-set>
        @if (!disabled()) {
          <flock-uuid-picker
            [kind]="uuidKind()!"
            label="Add"
            [name]="field().name"
            [(value)]="pendingUuid"
            (picked)="addUuid($event)"
          />
        }
      }
      @case ('map') {
        @for (entry of entries(); track $index) {
          <div class="entry">
            <mat-form-field>
              <mat-label>Key</mat-label>
              <input
                matInput
                [value]="entry[0]"
                [disabled]="disabled()"
                (input)="editKey($index, $any($event.target).value)"
              />
            </mat-form-field>
            @if (holdsSecretRefs() && !disabled()) {
              <mat-button-toggle-group
                class="value-kind"
                hideSingleSelectionIndicator
                aria-label="Kind of value"
                [value]="valueKind($index, entry[1])"
                (change)="switchValueKind($index, $event.value)"
              >
                <mat-button-toggle value="text">Text</mat-button-toggle>
                <mat-button-toggle value="secret">Secret</mat-button-toggle>
              </mat-button-toggle-group>
            }
            @if (valueKind($index, entry[1]) === 'secret' && !disabled()) {
              @defer {
                <flock-secret-ref-picker
                  [value]="format(entry[1])"
                  (picked)="editEntryValue($index, $event)"
                  (cleared)="editEntryValue($index, '')"
                />
              }
            } @else if (disabled() && pointsAtSecret(entry[1])) {
              <div class="secret-ref">
                <mat-icon>key</mat-icon>
                @defer {
                  <flock-secret-ref-link [value]="format(entry[1])" />
                }
              </div>
            } @else if (mapEnumValues().length > 0) {
              <mat-form-field>
                <mat-label>Value</mat-label>
                <mat-select
                  [value]="entry[1]"
                  [disabled]="disabled()"
                  (selectionChange)="editEntryValue($index, $event.value)"
                >
                  @for (option of mapEnumValues(); track option.number) {
                    <mat-option [value]="option.number">{{ option.name }}</mat-option>
                  }
                </mat-select>
              </mat-form-field>
            } @else {
              <mat-form-field>
                <mat-label>Value</mat-label>
                <input
                  matInput
                  [type]="holdsSecretValues() ? 'password' : 'text'"
                  [attr.autocomplete]="holdsSecretValues() ? 'new-password' : null"
                  [placeholder]="isStoredSecretKey(entry[0]) ? 'unchanged' : ''"
                  [value]="format(entry[1])"
                  [disabled]="disabled()"
                  (input)="editEntryText($index, $any($event.target).value)"
                />
              </mat-form-field>
            }
            @if (!disabled()) {
              <button
                mat-icon-button
                type="button"
                aria-label="Remove"
                (click)="removeEntry($index)"
              >
                <mat-icon>delete</mat-icon>
              </button>
            }
          </div>
        }
        @if (!disabled()) {
          <button mat-stroked-button type="button" [disabled]="hasEmptyKey()" (click)="addEntry()">
            Add
          </button>
        }
      }
      @case ('unsupported') {
        <p class="empty">This field can't be edited here.</p>
      }
    }
    @if (problem()) {
      <div class="field-error">{{ problem() }}</div>
    }
    @for (error of ownErrors(); track $index) {
      <div class="field-error">{{ error }}</div>
    }

    @if (editing(); as current) {
      <flock-side-panel
        #panel
        [title]="label()"
        [subject]="panelSubject()"
        [canClose]="canCloseEditor"
        (closed)="editing.set(null)"
      >
        <flock-proto-form
          [schema]="itemSchema()!"
          [(value)]="draft"
          [allReadonly]="editingReadonly()"
          [errors]="draftErrors()"
          [agentKinds]="agentKinds()"
          [policyNames]="policyNames()"
        />
        @if (editingGreyed()) {
          <p class="greyed" role="status">{{ editingGreyed() }}</p>
        }
        <div panelActions class="panel-actions">
          @if (!editingReadonly()) {
            <button mat-flat-button type="button" (click)="apply()">Apply to {{ owner() }}</button>
          }
          <button mat-button type="button" (click)="panel.close()">
            {{ editingReadonly() ? 'Close' : 'Cancel' }}
          </button>
        </div>
      </flock-side-panel>
    }
  `,
  styles: `
    .shortcut {
      margin: -8px 0 16px;
    }
    :host {
      display: block;
      margin-bottom: 16px;
    }
    .head {
      display: flex;
      align-items: center;
      gap: 4px;
      margin-bottom: 4px;
    }
    .label {
      font-weight: 500;
    }
    .help {
      font-size: 18px;
      width: 18px;
      height: 18px;
      color: var(--mat-sys-on-surface-variant);
    }
    .item,
    .entry {
      display: flex;
      align-items: center;
      gap: 8px;
    }
    .greyed {
      color: var(--mat-sys-on-surface-variant);
    }
    .item.invalid .open {
      color: var(--mat-sys-error);
    }
    mat-form-field {
      width: 100%;
    }
    .entry mat-form-field {
      flex: 1;
    }
    .value-kind {
      flex: none;
    }
    .secret-ref {
      display: flex;
      flex: 1;
      align-items: center;
      gap: 8px;
      min-width: 0;
      white-space: nowrap;
    }
    .empty {
      color: var(--mat-sys-on-surface-variant);
    }
    .field-error {
      color: var(--mat-sys-error);
      font-size: 0.85em;
    }
    .panel-actions {
      display: flex;
      gap: 8px;
    }
  `,
})
export class ListField {
  readonly field = input.required<DescField>();
  readonly value = input<unknown>();
  readonly disabled = input(false);
  // the errors under this field, their paths relative to it ([0].backend_uuid, ["key"] or the empty path)
  readonly errors = input<FieldError[]>([]);
  readonly agentKinds = input<AgentKinds | null>(null);
  readonly policyNames = input<string[] | null>(null);
  // the default external auth policy of the virtual host holding this list, which its routes inherit
  readonly hostPolicy = input('');
  // what Apply hands the item back to, in words (virtual server)
  readonly owner = input('');
  // why an item of a message list can't be changed or removed (a locked zone pool), empty when it can
  readonly itemGreyed = input<(item: Message) => string>(() => '');
  readonly valueChange = output<unknown>();

  private dialog = inject(MatDialog);

  protected readonly label = computed(() => fieldLabel(this.field()));
  protected readonly help = computed(() => fieldDoc(this.field()));
  protected readonly uuidKind = computed(() => uuidKindOf(this.field()));
  protected readonly problem = signal('');
  protected readonly pendingUuid = signal('');

  protected readonly mode = computed<Mode>(() => {
    const field = this.field();
    switch (field.fieldKind) {
      case 'message':
        return 'single';
      case 'map':
        return field.mapKind === 'message' ? 'unsupported' : 'map';
      case 'list':
        if (field.listKind === 'message') {
          return 'messages';
        }
        if (field.listKind === 'enum') {
          return 'enum-chips';
        }
        return this.uuidKind() ? 'uuids' : 'chips';
      default:
        return 'unsupported';
    }
  });

  protected readonly items = computed(() => (this.value() as unknown[] | undefined) ?? []);

  protected readonly itemSchema = computed<DescMessage | undefined>(() => {
    const field = this.field();
    const holdsMessages = field.fieldKind === 'message' || field.fieldKind === 'list';
    return holdsMessages ? field.message : undefined;
  });

  protected readonly enumValues = computed(() => {
    const field = this.field();
    return field.fieldKind === 'list' && field.listKind === 'enum' ? field.enum.values : [];
  });

  protected readonly mapEnumValues = computed(() => {
    const field = this.field();
    return field.fieldKind === 'map' && field.mapKind === 'enum' ? field.enum.values : [];
  });

  protected readonly ownErrors = computed(() =>
    this.errors()
      .filter((error) => error.path === '')
      .map((error) => error.message),
  );

  // The parent form hands a new copy of the map on every edit of any of its fields. Comparing the content keeps
  // the rows below from being reset by an edit elsewhere, which would drop a row whose key isn't typed yet.
  private readonly mapContent = computed(
    () => (this.value() as Record<string, unknown> | undefined) ?? {},
    { equal: sameEntries },
  );
  // the rows of a map, kept here while a key is being typed because two rows can share a key for a moment
  protected readonly entries = linkedSignal<[string, unknown][]>(() =>
    Object.entries(this.mapContent()),
  );
  protected readonly holdsSecretValues = computed(
    () => `${this.field().parent.typeName}.${this.field().name}` === secretDataField,
  );
  // the keys the map held when the form opened, which are the stored ones of a secret being edited
  private storedKeys: Set<string> | null = null;
  // whether the entries of this map can point at a secret, which only the top level metadata of the stored kinds can
  protected readonly holdsSecretRefs = computed(
    () =>
      this.field().name === 'metadata' && metadataWithSecretRefs.has(this.field().parent.typeName),
  );
  // The kind of value the user chose per row. A row never switched takes its kind from its value, and a row switched
  // keeps its kind, so a reference typed by hand in a Text row stays a text input under the cursor.
  private readonly chosenKinds = signal<Map<number, ValueKind>>(new Map());
  protected readonly hasEmptyKey = computed(() => this.entries().some(([key]) => key === ''));

  protected readonly editing = signal<Editing | null>(null);
  // replaced by a copy of the item on each open, the Empty message only fills the signal until then
  protected readonly draft = signal<Message>(create(EmptySchema));
  // set by Apply, so the panel closes without asking about the changes it just handed back
  private applied = false;

  protected readonly panelSubject = computed(() => {
    const editing = this.editing();
    if (!editing) {
      return '';
    }
    return editing.index === null ? 'new' : this.itemName(editing.original, editing.index);
  });

  // the reason the open item is greyed, it then opens read-only
  protected readonly editingGreyed = computed(() => {
    const editing = this.editing();
    const isStoredItem = editing !== null && editing.index !== null;
    return isStoredItem ? this.itemGreyed()(editing.original) : '';
  });
  protected readonly editingReadonly = computed(() => this.disabled() || !!this.editingGreyed());

  protected readonly draftErrors = computed(() => {
    const editing = this.editing();
    if (!editing || editing.index === null) {
      return [];
    }
    const prefix = this.mode() === 'single' ? '' : `[${editing.index}]`;
    return this.errors()
      .filter((error) => error.path.startsWith(prefix + '.'))
      .map((error) => ({ ...error, path: error.path.slice(prefix.length + 1) }));
  });

  protected isStoredSecretKey(key: string): boolean {
    if (!this.holdsSecretValues()) {
      return false;
    }
    this.storedKeys ??= new Set(Object.keys(this.mapContent()));
    return this.storedKeys.has(key);
  }

  protected asMessage(value: unknown): Message {
    return value as Message;
  }

  protected itemHasErrors(index: number): boolean {
    return this.errors().some((error) => error.path.startsWith(`[${index}]`));
  }

  // summary is the row of an item, its name followed by the policy checking it for a route
  protected summary(item: Message, index: number): string {
    const name = this.itemName(item, index);
    if (this.itemSchema()?.typeName !== RouteSchema.typeName) {
      return name;
    }
    const policy = effectivePolicy(item as Route, this.hostPolicy());
    return policy === '' ? name : `${name} · ${policy}`;
  }

  // itemName names an item by its name, else its first non empty text, else its position
  private itemName(item: Message, index: number): string {
    const schema = this.itemSchema();
    const fields = item as unknown as Record<string, unknown>;
    const texts = (schema?.fields ?? [])
      .filter((field) => field.fieldKind === 'scalar' && field.scalar === ScalarType.STRING)
      .map((field) => ({ name: field.name, text: fields[field.localName] as string }))
      .filter((entry) => entry.text !== '');
    const named = texts.find((entry) => entry.name === 'name') ?? texts[0];
    return named ? named.text : `${this.label()} ${index + 1}`;
  }

  protected format(value: unknown): string {
    const field = this.field();
    const scalar =
      field.fieldKind === 'list' || field.fieldKind === 'map' ? field.scalar : undefined;
    return scalar === undefined ? String(value) : formatScalar(scalar, value);
  }

  protected enumName(value: unknown): string {
    return this.enumValues().find((option) => option.number === value)?.name ?? String(value);
  }

  protected open(index: number | null) {
    const schema = this.itemSchema()!;
    const stored = index === null ? undefined : this.itemAt(index);
    const original = stored ?? create(schema);
    this.applied = false;
    // the panel edits a copy, the list keeps its item until Apply
    this.draft.set(clone(schema, original));
    this.editing.set({ index, original });
  }

  protected canCloseEditor = async (): Promise<boolean> => {
    const editing = this.editing();
    const untouched =
      editing === null || equals(this.itemSchema()!, editing.original, this.draft());
    if (this.applied || untouched) {
      return true;
    }
    const data: ConfirmData = {
      title: 'Drop the changes?',
      message: `The changes to this ${this.label().toLowerCase()} item are not applied yet.`,
      confirm: 'Drop',
    };
    return confirm(this.dialog, data);
  };

  protected apply() {
    const editing = this.editing();
    if (!editing) {
      return;
    }
    if (this.mode() === 'single') {
      this.valueChange.emit(this.draft());
    } else {
      const items = [...this.items()];
      if (editing.index === null) {
        items.push(this.draft());
      } else {
        items[editing.index] = this.draft();
      }
      this.valueChange.emit(items);
    }
    this.applied = true;
    // the panel goes away with the item applied, there is nothing left to ask about
    this.editing.set(null);
  }

  protected readonly shortcut = computed(
    () => shortcuts[`${this.field().parent.typeName}.${this.field().name}`] ?? null,
  );

  protected chipLabel(item: unknown): string {
    const shortcut = this.shortcut();
    const isShortcutValue = shortcut !== null && item === shortcut.value;
    return isShortcutValue ? shortcut.chip : this.format(item);
  }

  protected holdsOnly(item: string): boolean {
    const items = this.items();
    return items.length === 1 && items[0] === item;
  }

  protected remove(index: number) {
    const item = this.items()[index];
    const isMessage = this.mode() === 'messages';
    if (isMessage && this.itemGreyed()(item as Message)) {
      return;
    }
    this.valueChange.emit(this.items().filter((_, i) => i !== index));
  }

  protected clear() {
    this.valueChange.emit(undefined);
  }

  protected add(item: unknown) {
    this.valueChange.emit([...this.items(), item]);
  }

  protected addChip(event: MatChipInputEvent) {
    const field = this.field();
    const isLowercased = lowercasedFields.has(`${field.parent.typeName}.${field.name}`);
    const typed = event.value.trim();
    const text = isLowercased ? typed.toLowerCase() : typed;
    if (text === '') {
      return;
    }
    const parsed = parseScalar(field.scalar!, text, longAsString(field));
    if (!parsed.ok) {
      this.problem.set(`${text} is ${parsed.problem}`);
      return;
    }
    this.problem.set('');
    event.chipInput.clear();
    this.add(parsed.value);
  }

  protected addUuid(uuid: string) {
    this.add(uuid);
    this.pendingUuid.set('');
  }

  protected addEntry() {
    this.entries.update((entries) => [...entries, ['', this.mapZeroValue()]]);
  }

  protected pointsAtSecret(value: unknown): boolean {
    return this.holdsSecretRefs() && typeof value === 'string' && parseSecretRef(value) !== null;
  }

  protected valueKind(index: number, value: unknown): ValueKind {
    return this.chosenKinds().get(index) ?? (this.pointsAtSecret(value) ? 'secret' : 'text');
  }

  protected switchValueKind(index: number, kind: ValueKind) {
    this.chooseKind(index, kind);
    this.editEntryValue(index, '');
  }

  private chooseKind(index: number, kind: ValueKind) {
    this.chosenKinds.update((kinds) => new Map(kinds).set(index, kind));
  }

  protected removeEntry(index: number) {
    // the rows below the removed one move up by one
    this.chosenKinds.update(
      (kinds) =>
        new Map(
          [...kinds]
            .filter(([row]) => row !== index)
            .map(([row, kind]) => [row > index ? row - 1 : row, kind]),
        ),
    );
    this.entries.update((entries) => entries.filter((_, i) => i !== index));
    this.emitEntries();
  }

  protected editKey(index: number, key: string) {
    this.entries.update((entries) =>
      entries.map((entry, i) => (i === index ? [key, entry[1]] : entry)),
    );
    this.emitEntries();
  }

  protected editEntryValue(index: number, value: unknown) {
    this.entries.update((entries) =>
      entries.map((entry, i) => (i === index ? [entry[0], value] : entry)),
    );
    this.emitEntries();
  }

  protected editEntryText(index: number, text: string) {
    if (this.holdsSecretRefs()) {
      this.chooseKind(index, 'text');
    }
    const field = this.field();
    const parsed = parseScalar(field.scalar!, text);
    if (!parsed.ok) {
      this.problem.set(parsed.problem);
      return;
    }
    this.problem.set('');
    this.editEntryValue(index, parsed.value);
  }

  // A map is only emitted once its keys are distinct and filled. An object can't hold two rows with one key, and
  // emitting would merge them while the user is still typing.
  private emitEntries() {
    const keys = this.entries().map(([key]) => key);
    const duplicated = new Set(keys).size !== keys.length;
    if (keys.includes('') || duplicated) {
      this.problem.set(duplicated ? 'two entries have the same key' : 'an entry has no key');
      return;
    }
    // a Secret row with no key picked has no value to send yet, and an empty one would be stored as is
    const waitsForSecretKey = this.entries().some(
      ([, value], i) => this.valueKind(i, value) === 'secret' && !this.pointsAtSecret(value),
    );
    if (waitsForSecretKey) {
      this.problem.set('an entry has no secret key picked');
      return;
    }
    this.problem.set('');
    this.valueChange.emit(Object.fromEntries(this.entries()));
  }

  private mapZeroValue(): unknown {
    const field = this.field();
    if (field.fieldKind !== 'map' || field.mapKind === 'message') {
      return undefined;
    }
    if (field.mapKind === 'enum') {
      return field.enum.values[0]?.number ?? 0;
    }
    return scalarZeroValue(field.scalar, false);
  }

  private itemAt(index: number): Message | undefined {
    if (this.mode() === 'single') {
      return this.value() as Message | undefined;
    }
    return this.items()[index] as Message | undefined;
  }
}

function longAsString(field: DescField): boolean {
  return field.fieldKind === 'list' && field.listKind === 'scalar' && field.longAsString;
}

function sameEntries(a: Record<string, unknown>, b: Record<string, unknown>): boolean {
  const keys = Object.keys(a);
  const sameKeys = keys.length === Object.keys(b).length;
  return sameKeys && keys.every((key) => key in b && a[key] === b[key]);
}
