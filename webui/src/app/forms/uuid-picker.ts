import {
  Component,
  computed,
  ElementRef,
  inject,
  input,
  model,
  output,
  signal,
  viewChild,
} from '@angular/core';
import { MatAutocompleteModule } from '@angular/material/autocomplete';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatTooltipModule } from '@angular/material/tooltip';
import { DescField, ScalarType } from '@bufbuild/protobuf';
import { Api } from '../core/api';
import { ResourceCreations } from '../core/resource-creations';
import { KindName, kindWord, nameOrUuidFilter, Resource, resourceId } from '../core/resources';
import { Session } from '../core/session';
import { Access, adminReason, ControllerFeatures } from '../edit/access';

// The kind a *_uuid field points to, by the field name without _uuid and its plural s
const uuidKinds: Record<string, KindName> = {
  backend: 'backend',
  vip: 'vip',
  certificate: 'certificate',
  virtual_server: 'vs',
  vs: 'vs',
  zone: 'zone',
  instance: 'instance',
};

const pickerPageSize = 20;

// uuidKindOf gives the kind of resource a string field (or a repeated string) named *_uuid refers to, null when
// the field is something else
export function uuidKindOf(field: DescField): KindName | null {
  const scalar =
    field.fieldKind === 'scalar' || field.fieldKind === 'list' ? field.scalar : undefined;
  const isString = scalar === ScalarType.STRING;
  if (!isString || !field.name.endsWith('_uuid')) {
    return null;
  }
  const prefix = field.name.slice(0, -'_uuid'.length);
  const singular = prefix.endsWith('s') ? prefix.slice(0, -1) : prefix;
  return uuidKinds[prefix] ?? uuidKinds[singular] ?? null;
}

// UuidPicker is a text input searching the resources of one kind by name or UUID as the user types. Picking a
// resource writes its UUID, a UUID typed by hand is kept as it is. Its first entry creates a resource of the kind in a
// stacked panel and picks it once created.
@Component({
  selector: 'flock-uuid-picker',
  imports: [
    MatAutocompleteModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatTooltipModule,
  ],
  template: `
    <mat-form-field>
      <mat-label>{{ label() }}</mat-label>
      <input
        #textInput
        matInput
        [name]="name()"
        [value]="value()"
        [disabled]="disabled()"
        [matAutocomplete]="auto"
        placeholder="Search by name or type a UUID"
        (focus)="search($any($event.target).value)"
        (input)="typed($any($event.target).value)"
        (keydown.enter)="pickTyped($any($event.target).value)"
      />
      @if (help()) {
        <mat-icon matSuffix class="help" [matTooltip]="help()">info_outline</mat-icon>
      }
      @if (pickedName()) {
        <mat-hint>{{ pickedName() }}</mat-hint>
      }
      <mat-autocomplete #auto (optionSelected)="pick($event.option.value)">
        <!-- first, so a long list doesn't hide it; a null value tells it from a resource in pick() -->
        <mat-option
          class="create"
          [value]="null"
          [disabled]="!!createGreyed()"
          [matTooltip]="createGreyed()"
        >
          <mat-icon>add</mat-icon>
          Create a {{ kindWord(kind()) }}
        </mat-option>
        @for (resource of options(); track resource.UUID) {
          <mat-option [value]="resource.UUID">
            <span class="name">{{ resource.name }}</span>
            <span class="uuid">{{ resource.UUID }}</span>
          </mat-option>
        }
      </mat-autocomplete>
    </mat-form-field>
  `,
  styles: `
    :host {
      display: block;
    }
    mat-form-field {
      width: 100%;
    }
    .uuid {
      margin-left: 8px;
      font-size: 0.85em;
      color: var(--mat-sys-on-surface-variant);
    }
    .help {
      font-size: 18px;
      width: 18px;
      height: 18px;
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class UuidPicker {
  readonly kind = input.required<KindName>();
  readonly label = input('');
  readonly name = input('');
  readonly help = input('');
  readonly disabled = input(false);
  readonly value = model('');
  // emitted when the user settles on a UUID (picked in the list or typed then Enter), for the lists adding them
  readonly picked = output<string>();

  private api = inject(Api);
  protected readonly options = signal<Resource[]>([]);
  // the answer of an older search arriving late must not replace a newer one
  private searchCount = 0;

  protected readonly pickedName = computed(
    () => this.options().find((resource) => resource.UUID === this.value())?.name ?? '',
  );

  protected typed(text: string) {
    this.value.set(text);
    this.search(text);
  }

  protected readonly kindWord = kindWord;
  private readonly creations = inject(ResourceCreations);
  private readonly session = inject(Session);
  private readonly features = signal<ControllerFeatures>({ ipamEnabled: false });
  // the same reason the Create button of the page of the kind shows
  protected readonly createGreyed = computed(() => {
    const isAdmin = this.session.user()?.isAdmin ?? false;
    return adminReason(this.kind(), 'create', this.features(), isAdmin);
  });

  constructor() {
    inject(Access)
      .features()
      .then((features) => this.features.set(features))
      .catch(() => undefined);
  }
  private readonly textInput = viewChild.required<ElementRef<HTMLInputElement>>('textInput');

  protected pick(uuid: string | null) {
    if (uuid === null) {
      // the input keeps what was typed, the create entry is not a value
      this.textInput().nativeElement.value = this.value();
      this.creations.start({
        kind: this.kind(),
        created: (resource) => this.pickCreated(resource),
      });
      return;
    }
    this.value.set(uuid);
    this.picked.emit(uuid);
  }

  protected pickCreated(resource: Resource) {
    this.options.update((options) => [resource, ...options]);
    this.pick(resourceId(resource));
  }

  protected pickTyped(text: string) {
    if (text.trim() !== '') {
      this.picked.emit(text.trim());
    }
  }

  protected async search(text: string) {
    const count = ++this.searchCount;
    const search = text.trim();
    const filters = search === '' ? [] : [nameOrUuidFilter(search)];
    let found: Resource[];
    try {
      found = await this.api.list(this.kind(), { limit: pickerPageSize, offset: 0, filters });
    } catch {
      // the api already reported the failure, a UUID typed by hand still works without the list
      return;
    }
    if (count === this.searchCount) {
      this.options.set(found);
    }
  }
}
