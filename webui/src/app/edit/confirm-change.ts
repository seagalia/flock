import { Component, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { DescField, DescMessage, isFieldSet, Message, toJson } from '@bufbuild/protobuf';
import { base64Encode } from '@bufbuild/protobuf/wire';
import { Any, AnySchema } from '@bufbuild/protobuf/wkt';
import { firstValueFrom } from 'rxjs';
import { registry } from '../core/registry';

// One leaf of the resource that differs, before or after left out when the field is added or removed
export interface FieldChange {
  path: string;
  before?: string;
  after?: string;
}

export interface ChangeData {
  title: string;
  message: string;
  changes: FieldChange[];
  confirm: string;
}

// fieldChanges compares two versions of a message leaf by leaf, by their proto field names, the way `flock apply`
// prints its diff
export function fieldChanges(schema: DescMessage, before: Message, after: Message): FieldChange[] {
  const old = leaves(jsonOf(schema, before));
  const now = leaves(jsonOf(schema, after));
  const paths = [...new Set([...old.keys(), ...now.keys()])];
  return paths
    .filter((path) => old.get(path) !== now.get(path))
    .map((path) => ({ path, before: old.get(path), after: now.get(path) }));
}

// jsonOf is the proto JSON of a message, except that an Any the registry can't decode (an extension of an unknown
// agent kind, or bytes that don't parse) shows its type url and raw bytes, where toJson would throw
function jsonOf(schema: DescMessage, message: Message): unknown {
  if (schema.typeName === AnySchema.typeName) {
    return anyJson(message as Any);
  }
  // the well-known types have their own JSON forms (a duration as 5s), and hold no Any
  if (!schema.typeName.startsWith('flock.')) {
    return toJson(schema, message, { useProtoFieldName: true, registry });
  }
  const json: Record<string, unknown> = {};
  for (const field of schema.fields) {
    if (!isFieldSet(message, field)) {
      continue;
    }
    json[field.name] = fieldJson(field, valueOf(message, field));
  }
  return json;
}

function valueOf(message: Message, field: DescField): unknown {
  const fields = message as unknown as Record<string, unknown>;
  if (field.oneof) {
    return (fields[field.oneof.localName] as { value: unknown }).value;
  }
  return fields[field.localName];
}

function fieldJson(field: DescField, value: unknown): unknown {
  switch (field.fieldKind) {
    case 'message':
      return jsonOf(field.message, value as Message);
    case 'list':
      return (value as unknown[]).map((item) =>
        field.listKind === 'message' ? jsonOf(field.message, item as Message) : plain(item),
      );
    case 'map':
      return Object.fromEntries(
        Object.entries(value as Record<string, unknown>).map(([key, item]) => [
          key,
          field.mapKind === 'message' ? jsonOf(field.message, item as Message) : plain(item),
        ]),
      );
    case 'enum':
      return field.enum.value[value as number]?.name ?? value;
    case 'scalar':
      return plain(value);
  }
}

function plain(value: unknown): unknown {
  if (typeof value === 'bigint') {
    return value.toString();
  }
  if (value instanceof Uint8Array) {
    return base64Encode(value);
  }
  return value;
}

function anyJson(any: Any): unknown {
  try {
    return toJson(AnySchema, any, { useProtoFieldName: true, registry });
  } catch {
    return { '@type': any.typeUrl, value: base64Encode(any.value) };
  }
}

function leaves(json: unknown, path = '', found = new Map<string, string>()): Map<string, string> {
  // an empty message (a oneof member such as NoHealthCheck) or list has no leaf of its own, it is one
  const isEmpty =
    path !== '' && json !== null && typeof json === 'object' && Object.keys(json).length === 0;
  if (isEmpty) {
    found.set(path, Array.isArray(json) ? '[]' : '{}');
    return found;
  }
  if (Array.isArray(json)) {
    json.forEach((item, i) => leaves(item, `${path}[${i}]`, found));
    return found;
  }
  if (json !== null && typeof json === 'object') {
    for (const [key, value] of Object.entries(json)) {
      // a map key may hold dots, such as flock.autoprovision
      const step = /^\w+$/.test(key) ? `.${key}` : `[${JSON.stringify(key)}]`;
      leaves(value, path === '' ? step.replace(/^\./, '') : path + step, found);
    }
    return found;
  }
  found.set(path, JSON.stringify(json));
  return found;
}

@Component({
  selector: 'flock-confirm-change',
  imports: [MatButtonModule, MatDialogModule, MatIconModule],
  template: `
    <h2 mat-dialog-title>{{ data.title }}</h2>
    <mat-dialog-content>
      <p>{{ data.message }}</p>
      <ul class="changes">
        @for (change of data.changes; track change.path) {
          <li [attr.data-path]="change.path">
            <span class="path">{{ change.path }}</span>
            @if (change.before !== undefined) {
              <span class="before">{{ change.before }}</span>
            }
            @if (change.before !== undefined && change.after !== undefined) {
              <mat-icon>arrow_forward</mat-icon>
            }
            @if (change.after !== undefined) {
              <span class="after">{{ change.after }}</span>
            }
          </li>
        }
      </ul>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button type="button" [mat-dialog-close]="false">Cancel</button>
      <button mat-flat-button type="button" [mat-dialog-close]="true">{{ data.confirm }}</button>
    </mat-dialog-actions>
  `,
  styles: `
    .changes {
      margin: 0;
      padding: 0;
      list-style: none;
      font-family: var(--flock-mono);
    }
    li {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 6px;
      padding: 2px 0;
    }
    .path::after {
      content: ':';
    }
    .before {
      color: var(--mat-sys-error);
      text-decoration: line-through;
    }
    .after {
      color: var(--mat-sys-primary);
    }
    mat-icon {
      font-size: 16px;
      width: 16px;
      height: 16px;
    }
  `,
})
export class ConfirmChange {
  protected data = inject<ChangeData>(MAT_DIALOG_DATA);
}

export async function confirmChange(dialog: MatDialog, data: ChangeData): Promise<boolean> {
  const answer = await firstValueFrom(dialog.open(ConfirmChange, { data }).afterClosed());
  return answer === true;
}
