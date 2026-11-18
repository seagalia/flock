import {
  Component,
  computed,
  DestroyRef,
  effect,
  inject,
  input,
  linkedSignal,
  output,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Router } from '@angular/router';
import { clone, create, DescMessage, equals, Message } from '@bufbuild/protobuf';
import { EmptySchema } from '@bufbuild/protobuf/wkt';
import { CallOptions, ConnectError } from '@connectrpc/connect';
import { Role } from '../../gen/flock/api/config/permission/v1/permission_pb';
import { Api, silentCall } from '../core/api';
import { KindName, kindOf, kinds, kindWord, Resource, resourceId } from '../core/resources';
import { Session } from '../core/session';
import { FieldError, rulesProblems } from '../forms/rules';
import { violationsOf } from '../forms/violations';
import { confirm } from '../ui/confirm-dialog';
import { MenuItem } from '../ui/resource-menu';
import { SidePanel } from '../ui/side-panel';
import { agentKinds } from './agent-kinds';
import { PermissionsPanel } from './permissions-panel';
import { Access, adminReason, ControllerFeatures, RoleCache } from './access';
import { CardDef, cards } from './cards';
import { EditCard, SaveOutcome } from './card';
import { confirmChange, FieldChange, fieldChanges } from './confirm-change';
import { isLocked, lockMetadataKey, lockReason } from './lock';
import { clonePrefill, hasDocument, holdsUnknownAny } from './resource-doc';
import { UsedBy, usedBy } from './used-by';
import { proposeName } from '../core/names';
import { downloadFile } from '../core/download';

// The rules of one kind that the generic panel can't know, such as fields only an admin changes or fields saved
// through their own rpcs. kind-rules.ts holds them.
export interface KindRules {
  // fields the user can't change in this resource, on top of UUID
  readonly?: (resource: Resource | null) => string[];
  // checks of the kind on top of the lock key and the proto rules, run before a save and before a create (stored is
  // null then)
  problems?: (stored: Resource | null, edited: Resource) => FieldError[];
  // savers of the cards whose fields have their own rpcs, by card title. A saver replaces the confirmation and the
  // update rpc, it confirms each of its rpcs and gives the resource as stored after them, null when none ran.
  saveCard?: Record<string, (stored: Resource, edited: Resource) => Promise<Resource | null>>;
  // the ways to create a resource of the kind when there is more than one, the first one not greyed is chosen
  createWays?: CreateWay[];
  // why an item of a list field can't be changed, by field name (a locked zone pool), empty when it can
  itemGreyed?: Record<string, (item: Message) => string>;
  // fields whose values a confirmation never shows, such as a private key
  masked?: string[];
}

// FieldsFailure is what a kind rule throws when one of its rpcs failed or it refused the call, with the errors named
// after the fields of the form, since each rpc of a saver has its own request
export class FieldsFailure extends Error {
  constructor(
    readonly errors: FieldError[],
    message: string,
  ) {
    super(message);
  }
}

export interface CreateWay {
  label: string;
  // why the user can't take this way, empty when they can
  greyed: string;
  // the request this way sends instead of the kind's create rpc, null for the cards of the kind
  request: CreateRequest | null;
}

export interface CreateRequest {
  schema: DescMessage;
  send(request: Message, options: CallOptions): Promise<Resource | undefined>;
}

// The rpcs of one kind. field is the request field holding the resource, the controller names its violations from
// it.
interface KindCalls {
  field: string;
  read(api: Api, uuid: string): Promise<Resource | undefined>;
  create(api: Api, resource: Resource, options: CallOptions): Promise<Resource | undefined>;
  update(api: Api, resource: Resource, options: CallOptions): Promise<Resource | undefined>;
  delete(api: Api, uuid: string): Promise<unknown>;
}

export type Editable = Exclude<KindName, 'endpoint'>;

/* eslint-disable @typescript-eslint/no-explicit-any -- each kind's message type is checked by its own rpc */
export const calls: Record<Editable, KindCalls> = {
  backend: {
    field: 'backend',
    read: async (api, UUID) => (await api.backend.readBackend({ UUID })).backend,
    create: async (api, backend, options) =>
      (await api.backend.createBackend({ backend: backend as any }, options)).backend,
    update: async (api, backend, options) =>
      (await api.backend.updateBackend({ UUID: backend.UUID, backend: backend as any }, options))
        .backend,
    delete: (api, UUID) => api.backend.deleteBackend({ UUID }),
  },
  certificate: {
    field: 'certificate',
    read: async (api, UUID) => (await api.certificate.readCertificate({ UUID })).certificate,
    create: async (api, certificate, options) =>
      (await api.certificate.createCertificate({ certificate: certificate as any }, options))
        .certificate,
    update: async (api, certificate, options) =>
      (
        await api.certificate.updateCertificate(
          { UUID: certificate.UUID, certificate: certificate as any },
          options,
        )
      ).certificate,
    delete: (api, UUID) => api.certificate.deleteCertificate({ UUID }),
  },
  secret: {
    field: 'secret',
    read: async (api, UUID) => (await api.secret.readSecret({ UUID })).secret,
    create: async (api, secret, options) =>
      (await api.secret.createSecret({ secret: secret as any }, options)).secret,
    update: async (api, secret, options) =>
      (await api.secret.updateSecret({ UUID: secret.UUID, secret: secret as any }, options)).secret,
    delete: (api, UUID) => api.secret.deleteSecret({ UUID }),
  },
  vip: {
    field: 'vip',
    read: async (api, UUID) => (await api.vip.readVIP({ UUID })).vip,
    create: async (api, vip, options) =>
      (await api.vip.createVIP({ vip: vip as any }, options)).vip,
    update: async (api, vip, options) =>
      (await api.vip.updateVIP({ UUID: vip.UUID, vip: vip as any }, options)).vip,
    delete: (api, UUID) => api.vip.deleteVIP({ UUID }),
  },
  vs: {
    field: 'vs',
    read: async (api, UUID) => (await api.vs.readVS({ UUID })).vs,
    create: async (api, vs, options) => (await api.vs.createVS({ vs: vs as any }, options)).vs,
    update: async (api, vs, options) =>
      (await api.vs.updateVS({ UUID: vs.UUID, vs: vs as any }, options)).vs,
    delete: (api, UUID) => api.vs.deleteVS({ UUID }),
  },
  instance: {
    field: 'instance',
    read: async (api, UUID) => (await api.instance.readInstance({ UUID })).instance,
    create: async (api, instance, options) =>
      (await api.instance.createInstance({ instance: instance as any }, options)).instance,
    update: async (api, instance, options) =>
      (
        await api.instance.updateInstance(
          { UUID: instance.UUID, instance: instance as any },
          options,
        )
      ).instance,
    delete: (api, UUID) => api.instance.deleteInstance({ UUID }),
  },
  zone: {
    field: 'zone',
    read: async (api, UUID) => (await api.zone.readZone({ UUID })).zone,
    create: async (api, zone, options) =>
      (await api.zone.createZone({ zone: zone as any }, options)).zone,
    update: async (api, zone, options) =>
      (await api.zone.updateZone({ UUID: zone.UUID, zone: zone as any }, options)).zone,
    delete: (api, UUID) => api.zone.deleteZone({ UUID }),
  },
};
/* eslint-enable @typescript-eslint/no-explicit-any */

export const lockKeyRefusal =
  'The flock.autoprovision key is set by the automated owner of a resource only.';

function hasLockKey(resource: Message): boolean {
  const metadata = (resource as { metadata?: Record<string, string> }).metadata ?? {};
  return lockMetadataKey in metadata;
}

// withCard is the stored resource with only the card's fields taken from its draft, so a save never changes what
// the card doesn't show, even when the draft started from an older copy
function withCard(schema: DescMessage, stored: Resource, draft: Message, card: CardDef): Resource {
  const merged = clone(schema, stored) as unknown as Record<string, unknown>;
  const edited = clone(schema, draft) as unknown as Record<string, unknown>;
  for (const name of card.fields) {
    const member = schema.members.find((m) => m.name === name);
    if (member) {
      merged[member.localName] = edited[member.localName];
    }
  }
  return merged as unknown as Resource;
}

// isUnderCard tells whether an error path (endpoint, dns.name, routes[0].backend_uuid) falls in the card
function isUnderCard(schema: DescMessage, card: CardDef, path: string): boolean {
  const head = path.split(/[.[]/)[0];
  const oneof = schema.fields.find((field) => field.name === head)?.oneof;
  return card.fields.includes(head) || (oneof !== undefined && card.fields.includes(oneof.name));
}

function outcomeOf(err: unknown, requestField: string): SaveOutcome {
  if (err instanceof FieldsFailure) {
    return { done: false, errors: err.errors, problem: err.message };
  }
  const connectErr = ConnectError.from(err);
  const prefix = requestField + '.';
  const errors = violationsOf(connectErr).map((violation) => ({
    ...violation,
    path: violation.path.startsWith(prefix) ? violation.path.slice(prefix.length) : violation.path,
  }));
  return { done: false, errors, problem: connectErr.rawMessage };
}

// editMenuItems are the ⋮ menu items of a row the pages add, Clone for every kind and the resource file document
// for the kinds a resource file holds. Clone opens a Create, so it is greyed with createReason, empty when the user
// can create.
export function editMenuItems(
  resource: Resource,
  actions: { clone: (prefill: Resource) => void; copy: (text: string) => void },
  createReason: string,
): MenuItem[] {
  // a clone of a secret would only hold blank values, and no file of it may carry values
  if (kindOf(resource).name === 'secret') {
    return [];
  }
  const items: MenuItem[] = [
    {
      label: 'Clone',
      icon: 'file_copy',
      disabled: createReason || undefined,
      run: () => actions.clone(clonePrefill(resource)),
    },
  ];
  if (!hasDocument(resource)) {
    return items;
  }
  const unknownAny = holdsUnknownAny(kindOf(resource).schema, resource);
  // loaded with the menu so the click copies at once, a browser refuses a copy made long after the click
  const resourceFile = import('./resource-file');
  items.push({
    label: 'Copy as resource file document',
    icon: 'description',
    disabled: unknownAny
      ? `${resource.name} holds an extension this dashboard doesn't know, a resource file can't hold it.`
      : undefined,
    // built on click only, the menus of a table are made for every row
    run: async () => {
      const { resourceDocument } = await resourceFile;
      actions.copy(resourceDocument(resource)!);
    },
  });
  return items;
}

// ResourcePanel is the detail panel of one resource. Each card of it is edited and saved on its own, and the header
// holds Permissions and the lock. With no resource it creates one, from the prefill when Clone gave one.
@Component({
  selector: 'flock-resource-panel',
  imports: [
    EditCard,
    PermissionsPanel,
    MatButtonModule,
    MatButtonToggleModule,
    MatIconModule,
    MatTooltipModule,
    SidePanel,
  ],
  templateUrl: './resource-panel.html',
  styleUrl: './resource-panel.scss',
})
export class ResourcePanel {
  readonly kind = input.required<KindName>();
  readonly resource = input<Resource | null>(null);
  readonly prefill = input<Resource | null>(null);
  readonly rules = input<KindRules>({});
  readonly closed = output<void>();
  readonly changed = output<void>();
  // the resource a Create made, as the controller stored it
  readonly created = output<Resource>();

  private api = inject(Api);
  private access = inject(Access);
  private session = inject(Session);
  private dialog = inject(MatDialog);
  private router = inject(Router);
  private sidePanel = viewChild(SidePanel);

  // the copy the cards show, replaced by what each save stores
  protected readonly current = linkedSignal<Resource | null>(() => this.resource());
  protected readonly creating = computed(() => this.current() === null);
  protected readonly schema = computed(() => kinds[this.kind()].schema);
  protected readonly cardDefs = computed(() => cards[this.kind()]);
  protected readonly title = computed(() => {
    const label = kinds[this.kind()].label;
    return this.creating() ? `New ${label}` : label;
  });

  // the draft Create sends, every card edits it
  private readonly initialDraft = computed(() => {
    const prefill = this.prefill();
    const draft = prefill ? clone(this.schema(), prefill) : create(this.schema());
    // a clone comes without its name, a new resource always starts with a proposed one
    const named = draft as Message & { name?: string };
    if (named.name === '') {
      named.name = proposeName();
    }
    return draft;
  });
  protected readonly draft = linkedSignal<Message>(() => this.initialDraft());
  protected readonly createErrors = signal<FieldError[]>([]);
  protected readonly createProblem = signal('');
  protected readonly busy = signal(false);

  private readonly dirtyCards = signal(new Set<string>());
  protected readonly cache: RoleCache = new Map();
  private readonly features = signal<ControllerFeatures>({ ipamEnabled: false });
  private users: Promise<UsedBy[]> = Promise.resolve([]);
  protected readonly usedByList = signal<UsedBy[]>([]);
  protected readonly permissionsOpen = signal(false);

  private readonly currentUuid = computed(() => {
    const resource = this.current();
    return resource === null ? null : resourceId(resource);
  });

  protected readonly locked = computed(() => {
    const resource = this.current();
    return resource !== null && isLocked(resource as { metadata?: Record<string, string> });
  });
  protected readonly lockReason = lockReason;
  // a file of a secret would hold its values in clear
  protected readonly canDownloadFile = computed(() => this.kind() !== 'secret');
  protected readonly hasPermissions = computed(() => kinds[this.kind()].hasPermissions);

  protected readonly itemGreyed = computed(() => this.rules().itemGreyed ?? {});

  protected readonly readonlyFields = computed(() => [
    'UUID',
    ...(this.rules().readonly?.(this.current()) ?? []),
  ]);

  protected readonly editGreyed = computed(() => {
    if (this.locked()) {
      return lockReason;
    }
    return this.adminReason('update');
  });

  protected readonly createWays = computed(() => this.rules().createWays ?? []);
  protected readonly wayIndex = linkedSignal(() =>
    Math.max(
      this.createWays().findIndex((way) => !way.greyed),
      0,
    ),
  );
  private readonly way = computed<CreateWay | null>(
    () => this.createWays()[this.wayIndex()] ?? null,
  );
  protected readonly request = computed(() => this.way()?.request ?? null);
  // the draft of the request a create way sends, unused by the cards of the kind
  protected readonly requestDraft = linkedSignal<Message>(() =>
    create(this.request()?.schema ?? EmptySchema),
  );
  protected readonly requestCard = computed<CardDef | null>(() => {
    const request = this.request();
    if (request === null) {
      return null;
    }
    return {
      title: this.way()!.label,
      fields: request.schema.members.map((member) => member.name),
    };
  });

  protected readonly createGreyed = computed(
    () => this.way()?.greyed || this.adminReason('create'),
  );

  // a reload or a page left by the browser drops the dirty cards without canClose, so the browser asks instead
  private readonly warnBeforeUnload = (event: BeforeUnloadEvent) => event.preventDefault();

  constructor() {
    effect(() => {
      if (this.isDirty()) {
        window.addEventListener('beforeunload', this.warnBeforeUnload);
        return;
      }
      window.removeEventListener('beforeunload', this.warnBeforeUnload);
    });
    inject(DestroyRef).onDestroy(() =>
      window.removeEventListener('beforeunload', this.warnBeforeUnload),
    );
    this.access
      .features()
      .then((features) => this.features.set(features))
      .catch(() => undefined);
    // Keyed on the UUID, a save or a Reload of the same resource doesn't look again. A list call, not a permission
    // one, so it may run on opening.
    effect(() => {
      const uuid = this.currentUuid();
      this.usedByList.set([]);
      if (uuid === null) {
        return;
      }
      const resource = untracked(() => this.current())!;
      const request = usedBy(this.api, resource).catch(() => []);
      this.users = request;
      request.then((users) => {
        // an answer for a resource the panel no longer shows is dropped
        if (this.users === request) {
          this.usedByList.set(users);
        }
      });
    });
  }

  protected readonly canClose = async (): Promise<boolean> => {
    if (!this.isDirty()) {
      return true;
    }
    return confirm(this.dialog, {
      title: 'Drop your changes?',
      message: `The changes to this ${kindWord(this.kind())} are not saved yet.`,
      confirm: 'Drop',
    });
  };

  protected readonly loadAgentKinds = () => agentKinds(this.api, this.current()!);

  protected readonly allowEdit = async (): Promise<string | null> => {
    const denial = await this.access.check(this.current()!, Role.WRITER, this.cache);
    return denial?.reason ?? null;
  };

  // saveCard sends the stored resource with the card's changes, once the user saw them. It reads the resource
  // again first, so that a change made by someone else since the panel opened is shown instead of overwritten.
  protected readonly saveCard = async (card: CardDef, draft: Message): Promise<SaveOutcome> => {
    const shown = this.current()!;
    const schema = this.schema();
    const kindCalls = calls[this.kind() as Editable];
    const fresh = (await kindCalls.read(this.api, resourceId(shown))) ?? shown;
    const word = kindWord(this.kind());
    const untouched: SaveOutcome = { done: false, errors: [], problem: '' };

    if (!equals(schema, fresh, shown)) {
      const reload = await confirmChange(this.dialog, {
        title: 'Changed since you opened it',
        message: `This ${word} changed after it was shown here. Reload it to see the change, the edits of this card are then dropped.`,
        changes: this.masked(fieldChanges(schema, shown, fresh)),
        confirm: 'Reload',
      });
      if (!reload) {
        return untouched;
      }
      this.current.set(fresh);
      this.changed.emit();
      return { done: true };
    }

    const merged = withCard(schema, fresh, draft, card);
    if (equals(schema, fresh, merged)) {
      return { done: true };
    }
    const changes = this.masked(fieldChanges(schema, fresh, merged));
    const lockProblems: FieldError[] =
      hasLockKey(merged) && !hasLockKey(fresh)
        ? [{ path: 'metadata', message: lockKeyRefusal }]
        : [];
    const ruleProblems = [
      ...rulesProblems(schema, merged, '', { skipRequired: true }),
      ...(this.rules().problems?.(fresh, merged) ?? []),
    ].filter((problem) => isUnderCard(schema, card, problem.path));
    const problems = [...lockProblems, ...ruleProblems];
    if (problems.length > 0) {
      return { done: false, errors: problems, problem: '' };
    }
    const saver = this.rules().saveCard?.[card.title];
    if (!saver) {
      const agreed = await confirmChange(this.dialog, {
        title: `Save ${card.title}?`,
        message: `Saving stores the ${word} ${shown.name} with these changes.`,
        changes,
        confirm: 'Save',
      });
      if (!agreed) {
        return untouched;
      }
    }
    try {
      const saved = saver
        ? await saver(fresh, merged)
        : await kindCalls.update(this.api, merged, silentCall);
      // the user declined every rpc of the saver, the card keeps their changes
      if (saved === null) {
        return untouched;
      }
      this.current.set(saved ?? merged);
      this.changed.emit();
      return { done: true };
    } catch (err) {
      if (saver) {
        await this.showSavedPart(fresh);
      }
      return outcomeOf(err, kindCalls.field);
    }
  };

  // showSavedPart shows what the rpcs of a saver stored before one of them failed, the card keeps the user's changes
  private async showSavedPart(before: Resource) {
    const stored = await calls[this.kind() as Editable]
      .read(this.api, resourceId(before))
      .catch(() => undefined);
    const partlySaved = stored !== undefined && !equals(this.schema(), before, stored);
    if (!partlySaved) {
      return;
    }
    this.current.set(stored);
    this.changed.emit();
  }

  private masked(changes: FieldChange[]): FieldChange[] {
    const masked = this.rules().masked ?? [];
    return changes.map((change) => {
      const isMasked = masked.some(
        (name) =>
          change.path === name ||
          change.path.startsWith(name + '.') ||
          // a map key with a dot or a dash renders as data["k"]
          change.path.startsWith(name + '['),
      );
      if (!isMasked) {
        return change;
      }
      return {
        path: change.path,
        before: change.before === undefined ? undefined : '(stored value, hidden)',
        after: change.after === undefined ? undefined : '(new value, hidden)',
      };
    });
  }

  // showUser shows a resource using this one in the table of its page. The panel closes first, through canClose, so
  // leaving the page never drops a card being edited.
  protected async showUser(user: UsedBy) {
    const isClosed = (await this.sidePanel()?.close()) ?? true;
    if (!isClosed) {
      return;
    }
    await this.router.navigate([kinds[user.kind].link], {
      queryParams: { uuid: resourceId(user.resource) },
    });
  }

  protected kindLabel(kind: KindName): string {
    return kinds[kind].label;
  }

  protected markDirty(card: CardDef, dirty: boolean) {
    const next = new Set(this.dirtyCards());
    if (dirty) {
      next.add(card.title);
    } else {
      next.delete(card.title);
    }
    this.dirtyCards.set(next);
  }

  protected async createResource() {
    if (this.createGreyed() || this.busy()) {
      return;
    }
    const request = this.request();
    const problems = request
      ? rulesProblems(request.schema, this.requestDraft(), '', { skipRequired: true })
      : this.draftProblems();
    this.createErrors.set(problems);
    this.createProblem.set('');
    if (problems.length > 0) {
      this.createProblem.set('Some fields need a fix before creating.');
      return;
    }
    const kindCalls = calls[this.kind() as Editable];
    // the violations of a request name its own fields, those of a create rpc start with the resource field
    const requestField = request ? '' : kindCalls.field;
    this.busy.set(true);
    try {
      const draft = this.draft() as Resource;
      const created = request
        ? await request.send(this.requestDraft(), silentCall)
        : await kindCalls.create(this.api, draft, silentCall);
      this.current.set(created ?? draft);
      this.changed.emit();
      this.created.emit(created ?? draft);
    } catch (err) {
      const outcome = outcomeOf(err, requestField);
      if (!outcome.done) {
        this.createErrors.set(outcome.errors);
        this.createProblem.set(outcome.problem);
      }
    } finally {
      this.busy.set(false);
    }
  }

  // downloadYaml saves the draft as a file for the cli instead of creating it, after the same checks as Create
  protected async downloadYaml() {
    const draft = this.draft() as Resource;
    const problems = this.draftProblems();
    this.createErrors.set(problems);
    this.createProblem.set('');
    if (problems.length > 0) {
      this.createProblem.set('Some fields need a fix before downloading.');
      return;
    }
    if (holdsUnknownAny(this.schema(), draft)) {
      this.createProblem.set(
        "An extension of this resource is unknown to the dashboard, a file can't hold it.",
      );
      return;
    }
    const { resourceFile } = await import('./resource-file');
    const file = resourceFile(draft);
    downloadFile(file.name, file.text);
  }

  private draftProblems(): FieldError[] {
    const draft = this.draft() as Resource;
    const lockProblems: FieldError[] = hasLockKey(draft)
      ? [{ path: 'metadata', message: lockKeyRefusal }]
      : [];
    return [
      ...lockProblems,
      ...rulesProblems(this.schema(), draft, '', { skipRequired: true }),
      ...(this.rules().problems?.(null, draft) ?? []),
    ];
  }

  private isDirty(): boolean {
    if (!this.creating()) {
      return this.dirtyCards().size > 0;
    }
    const request = this.request();
    const requestEdited =
      request !== null && !equals(request.schema, create(request.schema), this.requestDraft());
    return requestEdited || !equals(this.schema(), this.initialDraft(), this.draft());
  }

  private adminReason(action: 'create' | 'update'): string {
    const isAdmin = this.session.user()?.isAdmin ?? false;
    return adminReason(this.kind(), action, this.features(), isAdmin);
  }
}
