import { Component, computed, input, output, signal } from '@angular/core';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { create, DescField, DescOneof, getOption, hasOption, ScalarType } from '@bufbuild/protobuf';
import { field as fieldRules } from '../../gen/buf/validate/validate_pb';
import { SecretKeyRef, SecretKeyRefSchema } from '../../gen/flock/type/v1/secret_pb';
import { parseSecretRef, secretRef } from '../core/secret-refs';
import { SecretRefLink, SecretRefPicker } from './secret-ref-picker';

const secretKeyRefType = 'flock.type.v1.SecretKeyRef';
// the rule id of a case only agents set, filled from the secret case, which users never write
const agentOnlyRule = 'agent_only';

export interface SecretOneofFields {
  // null when the plain case is set by agents only, the value then always comes from a secret
  text: DescField | null;
  ref: DescField;
}

function isAgentOnly(field: DescField): boolean {
  if (!hasOption(field, fieldRules)) {
    return false;
  }
  return getOption(field, fieldRules).cel.some((rule) => rule.id === agentOnlyRule);
}

// secretOneof recognises a oneof holding a value either as text or as a reference to a value of a secret (a header
// value, the password of a url), null for any other oneof.
export function secretOneof(oneof: DescOneof): SecretOneofFields | null {
  if (oneof.fields.length !== 2) {
    return null;
  }
  const text = oneof.fields.find((f) => f.fieldKind === 'scalar' && f.scalar === ScalarType.STRING);
  const ref = oneof.fields.find(
    (f) => f.fieldKind === 'message' && f.message.typeName === secretKeyRefType,
  );
  if (!text || !ref) {
    return null;
  }
  return { text: isAgentOnly(text) ? null : text, ref };
}

export interface OneofValue {
  case: string | undefined;
  value?: unknown;
}

type ValueKind = 'text' | 'secret';

// SecretKeyField edits a secret oneof as the metadata entries do: a Text / Secret toggle, then a text input or the
// secret and key selects. A oneof with no plain case for users shows the selects alone.
@Component({
  selector: 'flock-secret-key-field',
  imports: [
    MatButtonToggleModule,
    MatFormFieldModule,
    MatInputModule,
    SecretRefLink,
    SecretRefPicker,
  ],
  template: `
    <div class="line">
      @if (kind() === 'text') {
        <mat-form-field class="text">
          <mat-label>{{ label() }}</mat-label>
          <input
            matInput
            [name]="fields().text!.name"
            [value]="textValue()"
            [disabled]="disabled()"
            (input)="writeText($any($event.target).value)"
          />
        </mat-form-field>
      } @else if (disabled()) {
        <span class="label">{{ label() }}</span>
        @if (reference()) {
          <flock-secret-ref-link [value]="reference()" />
        }
      } @else {
        <flock-secret-ref-picker
          [value]="reference()"
          (picked)="writeRef($event)"
          (cleared)="valueChange.emit({ case: undefined })"
        />
      }
      @if (fields().text && !disabled()) {
        <mat-button-toggle-group
          class="value-kind"
          hideSingleSelectionIndicator
          [attr.aria-label]="'Kind of ' + label()"
          [value]="kind()"
          (change)="choose($event.value)"
        >
          <mat-button-toggle value="text">Text</mat-button-toggle>
          <mat-button-toggle value="secret">Secret</mat-button-toggle>
        </mat-button-toggle-group>
      }
    </div>
  `,
  styles: `
    .line {
      display: flex;
      align-items: baseline;
      gap: 8px;
    }
    .text {
      flex: 1;
    }
    .label {
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class SecretKeyField {
  readonly oneof = input.required<DescOneof>();
  readonly value = input.required<OneofValue>();
  readonly disabled = input(false);
  readonly valueChange = output<OneofValue>();

  protected readonly fields = computed(() => secretOneof(this.oneof())!);
  // value_source reads Value, password_source reads Password
  protected readonly label = computed(() => {
    const words = this.oneof()
      .name.replace(/_source$/, '')
      .replaceAll('_', ' ');
    return words.charAt(0).toUpperCase() + words.slice(1);
  });
  // the kind picked on the toggle, which only decides while no case is set: Secret empties the value until a key is
  // picked
  private readonly chosen = signal<ValueKind>('text');
  protected readonly kind = computed<ValueKind>(() => {
    const { text, ref } = this.fields();
    const selected = this.value().case;
    if (text === null || selected === ref.localName) {
      return 'secret';
    }
    return selected === text.localName ? 'text' : this.chosen();
  });
  protected readonly textValue = computed(() =>
    this.value().case === this.fields().text?.localName ? (this.value().value as string) : '',
  );
  protected readonly reference = computed(() => {
    if (this.value().case !== this.fields().ref.localName) {
      return '';
    }
    const keyRef = this.value().value as SecretKeyRef;
    return secretRef(keyRef.secretUuid, keyRef.key);
  });

  // a switch drops what the other kind held, a typed value must not stay behind a reference
  protected choose(kind: ValueKind) {
    this.chosen.set(kind);
    if (kind === 'text') {
      this.valueChange.emit({ case: this.fields().text!.localName, value: '' });
      return;
    }
    this.valueChange.emit({ case: undefined });
  }

  protected writeText(text: string) {
    this.valueChange.emit({ case: this.fields().text!.localName, value: text });
  }

  protected writeRef(reference: string) {
    const parsed = parseSecretRef(reference);
    if (parsed === null) {
      return;
    }
    const keyRef = create(SecretKeyRefSchema, { secretUuid: parsed.secretUuid, key: parsed.key });
    this.valueChange.emit({ case: this.fields().ref.localName, value: keyRef });
  }
}
