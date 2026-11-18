import { LocationStrategy } from '@angular/common';
import {
  Component,
  computed,
  effect,
  inject,
  input,
  linkedSignal,
  output,
  signal,
  viewChild,
} from '@angular/core';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatSelect, MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Router } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { Secret, SecretSchema } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { Api } from '../core/api';
import { ResourceCreations } from '../core/resource-creations';
import { kinds, listAll, nameOrUuidFilter, Resource } from '../core/resources';
import { parseSecretRef, secretRef } from '../core/secret-refs';
import { Session } from '../core/session';
import { Access, adminReason, ControllerFeatures } from '../edit/access';
import { SidePanels } from '../ui/side-panel';

// the value of the create entry of the secret select, which no uuid can equal
const createEntry = 'create a secret';

// SecretRefPicker points a metadata entry at one value of a secret, as two selects in the row of the entry: a secret
// among those the user can read, then one of its keys. It only ever sees keys, the list answer leaves the values empty.
// The first entry of the secret select creates a secret in a stacked panel, as the uuid pickers do.
@Component({
  selector: 'flock-secret-ref-picker',
  imports: [MatFormFieldModule, MatIconModule, MatSelectModule, MatTooltipModule],
  template: `
    <mat-form-field class="secret">
      <mat-label>Secret</mat-label>
      <mat-select
        #secretSelect
        [value]="secretUuid()"
        (selectionChange)="chooseSecret($event.value)"
      >
        <mat-option
          class="create"
          [value]="createEntry"
          [disabled]="!!createGreyed()"
          [matTooltip]="createGreyed()"
        >
          <mat-icon>add</mat-icon>
          Create a secret
        </mat-option>
        @for (secret of secrets(); track secret.UUID) {
          <mat-option [value]="secret.UUID">{{ secret.name }}</mat-option>
        }
        @if (isUnlisted()) {
          <mat-option [value]="secretUuid()">{{ secretUuid() }}</mat-option>
        }
      </mat-select>
    </mat-form-field>
    <mat-form-field class="key">
      <mat-label>Key</mat-label>
      <mat-select
        [value]="key()"
        [disabled]="keys().length === 0"
        (selectionChange)="picked.emit(ref($event.value))"
      >
        @for (key of keys(); track key) {
          <mat-option [value]="key">{{ key }}</mat-option>
        }
      </mat-select>
    </mat-form-field>
  `,
  styles: `
    :host {
      display: flex;
      flex: 1;
      gap: 8px;
      min-width: 0;
    }
    .secret {
      flex: 3;
    }
    .key {
      flex: 2;
    }
  `,
})
export class SecretRefPicker {
  // the reference the entry holds, empty while no key is picked
  readonly value = input('');
  readonly picked = output<string>();
  // the secret changed, so the reference the entry holds no longer matches what the selects show
  readonly cleared = output<void>();

  private api = inject(Api);
  private readonly creations = inject(ResourceCreations);
  private readonly session = inject(Session);
  private readonly features = signal<ControllerFeatures>({ ipamEnabled: false });
  private readonly secretSelect = viewChild.required<MatSelect>('secretSelect');
  protected readonly createEntry = createEntry;
  // the same reason the Create button of the secrets page shows
  protected readonly createGreyed = computed(() => {
    const isAdmin = this.session.user()?.isAdmin ?? false;
    return adminReason('secret', 'create', this.features(), isAdmin);
  });
  protected readonly secrets = signal<Secret[]>([]);
  private readonly parsed = computed(() => parseSecretRef(this.value()));
  protected readonly secretUuid = linkedSignal(() => this.parsed()?.secretUuid ?? '');
  protected readonly key = computed(() =>
    this.parsed()?.secretUuid === this.secretUuid() ? this.parsed()!.key : '',
  );
  protected readonly keys = computed(() => {
    const secret = this.secrets().find((s) => s.UUID === this.secretUuid());
    return Object.keys(secret?.data ?? {});
  });
  // a secret the entry points at but the user can't read still shows, by its uuid
  protected readonly isUnlisted = computed(
    () => this.secretUuid() !== '' && !this.secrets().some((s) => s.UUID === this.secretUuid()),
  );

  constructor() {
    // the api already reported a failure, the picker then stays empty
    listAll(
      (limit, offset) =>
        this.api.list('secret', { limit, offset, filters: [] }) as Promise<Secret[]>,
    )
      .then((secrets) => this.secrets.set(secrets))
      .catch(() => undefined);
    inject(Access)
      .features()
      .then((features) => this.features.set(features))
      .catch(() => undefined);
  }

  protected chooseSecret(uuid: string) {
    if (uuid === createEntry) {
      // the create entry is not a value, the select goes back to the secret it showed
      this.secretSelect().value = this.secretUuid();
      this.creations.start({ kind: 'secret', created: (resource) => this.pickCreated(resource) });
      return;
    }
    this.secretUuid.set(uuid);
    this.cleared.emit();
  }

  // The values of the created secret are dropped, the picker only ever holds keys. A secret of one key leaves nothing
  // to choose, so its key is picked at once.
  private pickCreated(resource: Resource) {
    const created = resource as Secret;
    const keys = Object.keys(created.data);
    const withoutValues = create(SecretSchema, {
      UUID: created.UUID,
      name: created.name,
      data: Object.fromEntries(keys.map((key) => [key, ''])),
    });
    this.secrets.update((secrets) => [withoutValues, ...secrets]);
    this.chooseSecret(created.UUID);
    if (keys.length === 1) {
      this.picked.emit(this.ref(keys[0]));
    }
  }

  protected ref(key: string): string {
    return secretRef(this.secretUuid(), key);
  }
}

// SecretRefLink shows a reference as the name of its secret and the key, a link to the secret in its table. The
// uuid stands in for the name of a secret the user can't read.
@Component({
  selector: 'flock-secret-ref-link',
  template: `
    @if (parsed(); as ref) {
      <a [href]="href(ref.secretUuid)" (click)="show($event, ref.secretUuid)">
        {{ secretName() || ref.secretUuid }} / {{ ref.key }}
      </a>
    }
  `,
})
export class SecretRefLink {
  readonly value = input.required<string>();

  private api = inject(Api);
  private router = inject(Router);
  private location = inject(LocationStrategy);
  private panels = inject(SidePanels);
  private readonly link = kinds.secret.link;
  protected readonly parsed = computed(() => parseSecretRef(this.value()));
  protected readonly secretName = signal('');

  constructor() {
    effect(() => {
      const uuid = this.parsed()?.secretUuid;
      if (uuid === undefined) {
        return;
      }
      this.api
        .list('secret', { limit: 1, offset: 0, filters: [nameOrUuidFilter(uuid)] })
        .then((found) => this.secretName.set(found.find((s) => s.UUID === uuid)?.name ?? ''))
        // the api already reported the failure, the uuid then stands in for the name
        .catch(() => undefined);
    });
  }

  protected href(uuid: string): string {
    // the base href (/dashboard/) must be in the href for a middle click or "open in a new tab"
    const url = this.router.serializeUrl(
      this.router.createUrlTree([this.link], { queryParams: { uuid } }),
    );
    return this.location.prepareExternalUrl(url);
  }

  // A routerLink would navigate on its own click handler whatever this one decides, so the navigation happens here,
  // once the open panels have closed through their canClose and no edit in progress is dropped.
  protected async show(event: Event, uuid: string) {
    event.preventDefault();
    if (!(await this.panels.closeAbove(0))) {
      return;
    }
    await this.router.navigate([this.link], { queryParams: { uuid } });
  }
}
