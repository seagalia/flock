import { Component, computed, input, output, signal } from '@angular/core';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import {
  DescField,
  DescMessage,
  fromJson,
  JsonValue,
  ScalarType,
  toJson,
} from '@bufbuild/protobuf';
import { FeatureSet_FieldPresence, isWrapperDesc } from '@bufbuild/protobuf/wkt';
import { base64Decode, base64Encode } from '@bufbuild/protobuf/wire';
import { fieldDefaults, fieldDocs } from '../core/proto-docs.gen';
import { registry } from '../core/registry';
import { ProposedName } from '../ui/proposed-name';

// the words of field names written as acronyms, so a label reads HTTP health check and not Http health check
const acronyms: Record<string, string> = Object.fromEntries(
  [
    'API',
    'ARP',
    'BGP',
    'CA',
    'CIDR',
    'DNS',
    'EDS',
    'gRPC',
    'HTTP',
    'HTTP2',
    'HTTPS',
    'ID',
    'IP',
    'IPAM',
    'IPv4',
    'IPv6',
    'JSON',
    'L3',
    'L4',
    'L7',
    'LB',
    'LDAP',
    'mTLS',
    'NDP',
    'OIDC',
    'SNI',
    'TCP',
    'TLS',
    'TTL',
    'UDP',
    'URI',
    'URL',
    'UUID',
    'VIP',
    'VIPs',
  ].map((acronym) => [acronym.toLowerCase(), acronym]),
);

// fieldLabel humanizes a proto field name, backend_uuid gives Backend and listen_port gives Listen port
export function fieldLabel(field: DescField): string {
  const words = field.name.replace(/_uuid$/, '').split('_');
  const shown = words.map((word, index) => {
    const acronym = acronyms[word.toLowerCase()];
    if (acronym) {
      // an acronym keeps its own case first too, gRPC health check
      return acronym;
    }
    return index === 0 ? word.charAt(0).toUpperCase() + word.slice(1) : word;
  });
  return shown.join(' ');
}

// messageLabel humanizes a message name for sentences, VirtualServer gives virtual server and VIP stays VIP
export function messageLabel(schema: DescMessage): string {
  return schema.name
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .split(' ')
    .map((word) => (/^[A-Z0-9]+$/.test(word) ? word : word.toLowerCase()))
    .join(' ');
}

// fieldDoc is the proto comment of the field, protobuf-es strips them from the descriptors it embeds
export function fieldDoc(field: DescField): string {
  return fieldDocs[`${field.parent.typeName}.${field.name}`] ?? '';
}

export type Parsed = { ok: true; value: unknown } | { ok: false; problem: string };

const intTypes32 = [
  ScalarType.INT32,
  ScalarType.UINT32,
  ScalarType.SINT32,
  ScalarType.FIXED32,
  ScalarType.SFIXED32,
];
const intTypes64 = [
  ScalarType.INT64,
  ScalarType.UINT64,
  ScalarType.SINT64,
  ScalarType.FIXED64,
  ScalarType.SFIXED64,
];

// parseScalar turns the text of an input into the value protobuf-es holds for that scalar type
export function parseScalar(scalar: ScalarType, text: string, longAsString = false): Parsed {
  if (scalar === ScalarType.STRING) {
    return { ok: true, value: text };
  }
  if (scalar === ScalarType.BOOL) {
    return { ok: true, value: text === 'true' };
  }
  if (scalar === ScalarType.BYTES) {
    try {
      return { ok: true, value: base64Decode(text) };
    } catch {
      return { ok: false, problem: 'not base64' };
    }
  }
  const trimmed = text.trim() === '' ? '0' : text.trim();
  if (intTypes64.includes(scalar)) {
    // 64 bits integers are bigints, a javascript number would round them
    if (!/^-?\d+$/.test(trimmed)) {
      return { ok: false, problem: 'not an integer' };
    }
    return { ok: true, value: longAsString ? trimmed : BigInt(trimmed) };
  }
  const number = Number(trimmed);
  if (Number.isNaN(number)) {
    return { ok: false, problem: 'not a number' };
  }
  if (intTypes32.includes(scalar) && !Number.isInteger(number)) {
    return { ok: false, problem: 'not an integer' };
  }
  return { ok: true, value: number };
}

export function formatScalar(scalar: ScalarType, value: unknown): string {
  if (value === undefined || value === null) {
    return '';
  }
  if (scalar === ScalarType.BYTES) {
    return base64Encode(value as Uint8Array);
  }
  return String(value);
}

// The messages edited as one text in their JSON form, so a duration reads 300s and a timestamp is RFC 3339
const textMessages: Record<string, string> = {
  'google.protobuf.Duration': 'a duration like 300s',
  'google.protobuf.Timestamp': 'a date like 2026-10-31T12:00:00Z',
};

// the string fields holding a PEM block (certificates, CAs, keys), many lines an input would show as one
const pemFields = new Set(['ca', 'cert_chain', 'private_key']);

type InputKind =
  | 'text'
  | 'pem'
  | 'number'
  | 'bool'
  | 'enum'
  | 'message-text'
  | 'struct'
  | 'unsupported';

// FieldInput edits one field holding a single value, such as a scalar, an enum, a wrapper, a duration, a timestamp or a
// struct. It emits the new value of the field, the form writes it in its copy of the message.
@Component({
  selector: 'flock-field-input',
  imports: [
    ProposedName,
    MatCheckboxModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatTooltipModule,
  ],
  template: `
    @switch (kind()) {
      @case ('bool') {
        <div class="bool">
          <mat-checkbox
            [name]="field().name"
            [checked]="value() === true"
            [disabled]="disabled()"
            (change)="valueChange.emit($event.checked)"
          >
            {{ label() }}
          </mat-checkbox>
          @if (help()) {
            <mat-icon class="help" [matTooltip]="help()">info_outline</mat-icon>
          }
        </div>
      }
      @case ('enum') {
        <mat-form-field>
          <mat-label>{{ label() }}</mat-label>
          <mat-select
            [attr.name]="field().name"
            [value]="value()"
            [disabled]="disabled()"
            (selectionChange)="valueChange.emit($event.value)"
          >
            @for (option of enumValues(); track option.number) {
              <mat-option [value]="option.number">{{ option.name }}</mat-option>
            }
          </mat-select>
          @if (help()) {
            <mat-icon matSuffix class="help" [matTooltip]="help()">info_outline</mat-icon>
          }
        </mat-form-field>
      }
      @case ('struct') {
        <mat-form-field class="wide">
          <mat-label>{{ label() }} (JSON)</mat-label>
          <textarea
            matInput
            [name]="field().name"
            [value]="text()"
            [disabled]="disabled()"
            (input)="edit($any($event.target).value)"
          ></textarea>
          @if (help()) {
            <mat-icon matSuffix class="help" [matTooltip]="help()">info_outline</mat-icon>
          }
        </mat-form-field>
      }
      @case ('pem') {
        <mat-form-field>
          <mat-label>{{ label() }}</mat-label>
          <textarea
            matInput
            class="pem"
            rows="6"
            [name]="field().name"
            [value]="text()"
            [disabled]="disabled()"
            (input)="edit($any($event.target).value)"
          ></textarea>
          @if (help()) {
            <mat-icon matSuffix class="help" [matTooltip]="help()">info_outline</mat-icon>
          }
        </mat-form-field>
      }
      @case ('unsupported') {
        <p class="unsupported">{{ label() }} can't be edited here.</p>
      }
      @default {
        <mat-form-field [floatLabel]="placeholder() ? 'always' : 'auto'">
          <mat-label>{{ label() }}</mat-label>
          <input
            matInput
            flockProposedName
            [name]="field().name"
            [type]="kind() === 'number' ? 'number' : 'text'"
            [value]="text()"
            [disabled]="disabled()"
            [placeholder]="placeholder()"
            (input)="edit($any($event.target).value)"
          />
          @if (help()) {
            <mat-icon matSuffix class="help" [matTooltip]="help()">info_outline</mat-icon>
          }
        </mat-form-field>
      }
    }
    @if (problem()) {
      <div class="field-error">{{ problem() }}</div>
    }
    @for (error of errors(); track $index) {
      <div class="field-error">{{ error }}</div>
    }
  `,
  styles: `
    :host {
      display: block;
    }
    mat-form-field {
      width: 100%;
    }
    .bool {
      display: flex;
      align-items: center;
      gap: 4px;
      margin-bottom: 12px;
    }
    .help {
      font-size: 18px;
      width: 18px;
      height: 18px;
      color: var(--mat-sys-on-surface-variant);
    }
    .field-error {
      color: var(--mat-sys-error);
      font-size: 0.85em;
      margin: -12px 0 12px 16px;
    }
    .pem {
      font-family: monospace;
      font-size: 0.85em;
    }
    .unsupported {
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class FieldInput {
  readonly field = input.required<DescField>();
  readonly value = input<unknown>();
  readonly disabled = input(false);
  readonly errors = input<string[]>([]);
  readonly valueChange = output<unknown>();

  protected readonly label = computed(() => fieldLabel(this.field()));
  protected readonly help = computed(() => fieldDoc(this.field()));
  // the text typed that doesn't parse, shown under the field until it does
  protected readonly problem = signal('');

  protected readonly kind = computed<InputKind>(() => {
    const field = this.field();
    if (field.fieldKind === 'enum') {
      return 'enum';
    }
    const isPem =
      field.fieldKind === 'scalar' &&
      field.scalar === ScalarType.STRING &&
      pemFields.has(field.name);
    if (isPem) {
      return 'pem';
    }
    if (field.fieldKind === 'scalar') {
      return scalarKind(field.scalar);
    }
    if (field.fieldKind !== 'message') {
      return 'unsupported';
    }
    if (field.message.typeName === 'google.protobuf.Struct') {
      return 'struct';
    }
    if (textMessages[field.message.typeName]) {
      return 'message-text';
    }
    if (isWrapperDesc(field.message)) {
      return scalarKind(field.message.fields[0].scalar!);
    }
    return 'unsupported';
  });

  protected readonly enumValues = computed(() => {
    const field = this.field();
    return field.fieldKind === 'enum' ? field.enum.values : [];
  });

  protected readonly placeholder = computed(() => {
    const field = this.field();
    const byDefault = fieldDefaults[`${field.parent.typeName}.${field.name}`];
    if (byDefault) {
      return `${byDefault} by default`;
    }
    return field.fieldKind === 'message' ? (textMessages[field.message.typeName] ?? '') : '';
  });

  protected readonly text = computed(() => {
    const field = this.field();
    const value = this.value();
    if (value === undefined) {
      return '';
    }
    if (field.fieldKind === 'scalar') {
      // without presence a zero is the field left unset, an empty input shows the default instead of a 0 never sent
      const isUnset =
        field.presence === FeatureSet_FieldPresence.IMPLICIT && (value === 0 || value === 0n);
      return isUnset ? '' : formatScalar(field.scalar, value);
    }
    if (field.fieldKind !== 'message') {
      return '';
    }
    if (field.message.typeName === 'google.protobuf.Struct') {
      return JSON.stringify(value, null, 2);
    }
    if (textMessages[field.message.typeName]) {
      return String(toJson(field.message, value as never, { registry }));
    }
    return formatScalar(field.message.fields[0].scalar!, value);
  });

  protected edit(text: string) {
    const parsed = this.parse(text);
    if (!parsed.ok) {
      this.problem.set(parsed.problem);
      return;
    }
    this.problem.set('');
    this.valueChange.emit(parsed.value);
  }

  private parse(text: string): Parsed {
    const field = this.field();
    if (field.fieldKind === 'scalar') {
      return parseScalar(field.scalar, text, field.longAsString);
    }
    if (field.fieldKind !== 'message') {
      return { ok: false, problem: 'not editable' };
    }
    // a field with presence left empty is unset, which differs from a zero duration or an empty string
    if (text.trim() === '') {
      return { ok: true, value: undefined };
    }
    if (field.message.typeName === 'google.protobuf.Struct') {
      return parseStruct(text);
    }
    const expected = textMessages[field.message.typeName];
    if (expected) {
      try {
        return { ok: true, value: fromJson(field.message, text.trim() as JsonValue) };
      } catch {
        return { ok: false, problem: `expected ${expected}` };
      }
    }
    return parseScalar(field.message.fields[0].scalar!, text);
  }
}

function scalarKind(scalar: ScalarType): InputKind {
  if (scalar === ScalarType.BOOL) {
    return 'bool';
  }
  const textual = scalar === ScalarType.STRING || scalar === ScalarType.BYTES;
  // 64 bits integers stay text, a number input would round them
  const wide = intTypes64.includes(scalar);
  return textual || wide ? 'text' : 'number';
}

function parseStruct(text: string): Parsed {
  try {
    const parsed: unknown = JSON.parse(text);
    const isObject = typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed);
    return isObject
      ? { ok: true, value: parsed }
      : { ok: false, problem: 'expected a JSON object' };
  } catch {
    return { ok: false, problem: 'not JSON' };
  }
}
