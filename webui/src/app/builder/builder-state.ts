import { computed, inject, Injectable, signal } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { create, type Message } from '@bufbuild/protobuf';
import { Backend } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { Certificate } from '../../gen/flock/api/config/cert/v1/cert_pb';
import { Instance } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { Role } from '../../gen/flock/api/config/permission/v1/permission_pb';
import { VIP, VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { Zone } from '../../gen/flock/api/config/zone/v1/zone_pb';
import { Api } from '../core/api';
import { CliHost } from '../core/cli-host';
import { ResourceCreations } from '../core/resource-creations';
import { KindName, listAll } from '../core/resources';
import { Session } from '../core/session';
import { Access, adminReason, ControllerFeatures, RoleCache } from '../edit/access';
import { kindRules } from '../edit/kind-rules';
import { isLocked } from '../edit/lock';
import { CreateWay } from '../edit/resource-panel';
import {
  CreationOutcome,
  CreationPlan,
  creationActions,
  errorMessage,
  runActions,
  VipPlan,
} from './create-resources';
import {
  buildDocuments,
  newFile,
  fieldProblems,
  vipProblems,
  type BackendInput,
  type DocKind,
  type NewVipInput,
  type Protocol,
  type Refs,
  type WizardInput,
} from './documents';
import { proposeName } from '../core/names';

export type BuilderMode = 'cli' | 'controller';
export type BackendMode = 'create' | 'existing';
export type Output = 'create' | 'file';
export type VsInput = WizardInput['vs'];

export const steps = { vip: 0, vs: 1, backend: 2, review: 3 };

// newVipChoice is the VIP pick of a run creating its VIP, no VIP is the empty pick and an existing one its uuid
export const newVipChoice = 'new';

// createChoice is the first entry of the selects picking an instance, a zone or a certificate, which no uuid can equal
export const createChoice = 'create';

export const newFileName = 'resources.yml';

// a VIP from the ipam has no ip until RegisterVip gives it one, a vip document can't hold it
export const ipamFileReason =
  'A file holds a vip document with an explicit IP, a VIP from the ipam is only created directly.';

const defaultPorts: Record<Protocol, number> = { http: 80, https: 443, tls: 443, tcp: 0, udp: 0 };

function emptyVs(): VsInput {
  return {
    name: proposeName(),
    protocol: 'http',
    port: 80,
    domains: [],
    certificateUuid: '',
    passthrough: false,
  };
}

function emptyBackend(): BackendInput {
  return {
    name: proposeName(),
    discovery: 'list',
    endpoints: [{ ip: '', port: 0, weight: 0 }],
    dns: { host: '', port: 0 },
    eds: { url: '', serviceName: '', passwordRef: '' },
    tls: false,
    healthCheck: { type: 'tcp', path: '/' },
  };
}

function emptyVip(): NewVipInput {
  return {
    name: proposeName(),
    way: 'ipam',
    ip: '',
    target: 'instance',
    instanceUuid: '',
    zoneUuid: '',
    pool: '',
  };
}

function byName(a: { name: string }, b: { name: string }): number {
  return a.name.localeCompare(b.name);
}

// vipWayOf names a create way of the VIPs page, only the ipam way sends its own request
export function vipWayOf(way: CreateWay): NewVipInput['way'] {
  return way.request !== null ? 'ipam' : 'explicit';
}

// BuilderState holds the run of the builder wizard. Each opening of the wizard starts a new run, with new proposed
// names. It lives in the root injector so the mode of the page is found once.
@Injectable({ providedIn: 'root' })
export class BuilderState {
  private api = inject(Api);
  private cli = inject(CliHost);
  private access = inject(Access);
  private session = inject(Session);
  private dialog = inject(MatDialog);
  private creations = inject(ResourceCreations);

  readonly mode = signal<BuilderMode | null>(null);
  readonly cwd = signal('');
  readonly step = signal(steps.vip);
  // vipPick is '' for no VIP, newVipChoice for a VIP the run creates, or the uuid of an existing VIP
  readonly vipPick = signal('');
  readonly newVip = signal<NewVipInput>(emptyVip());
  readonly vs = signal<VsInput>(emptyVs());
  readonly backendMode = signal<BackendMode>('create');
  readonly existingBackendUuid = signal('');
  readonly newBackend = signal<BackendInput>(emptyBackend());
  readonly output = signal<Output>('create');
  // savedPath is where the cli saved the file of the run, the review then shows how to apply it
  readonly savedPath = signal('');
  // savePath is where the cli saves, kept across runs
  readonly savePath = signal(newFileName);
  readonly outcome = signal<CreationOutcome | null>(null);

  readonly vips = signal<VIP[]>([]);
  // the dashboard never changes a locked VIP, so the run can't add its virtual server to one
  readonly selectableVips = computed(() => this.vips().filter((vip) => !isLocked(vip)));
  readonly pickedVip = computed(() => this.vips().find((vip) => vip.UUID === this.vipPick()));
  // the store keeps no name unique, and flock apply refuses a vip document whose name matches several VIPs
  readonly vipNameCount = computed(() => {
    const name = this.pickedVip()?.name;
    return this.vips().filter((candidate) => candidate.name === name).length;
  });
  readonly vipDenial = signal('');
  readonly checkingVip = signal(false);
  // the ways to create a VIP, the same as on the VIPs page
  readonly vipWays = signal<CreateWay[]>([]);
  readonly newVipGreyed = computed(() => {
    const isOpen = this.vipWays().some((way) => way.greyed === '');
    return isOpen ? '' : (this.vipWays()[0]?.greyed ?? '');
  });
  readonly instances = signal<Instance[]>([]);
  readonly zones = signal<Zone[]>([]);
  private certificateList = signal<Certificate[]>([]);
  private backendList = signal<Backend[]>([]);
  // the pickers show names and keep uuids, two resources may share a name
  readonly certificates = computed(() => [...this.certificateList()].sort(byName));
  readonly controllerBackends = computed(() => [...this.backendList()].sort(byName));
  private controllerVs = signal<string[]>([]);
  private features = signal<ControllerFeatures>({ ipamEnabled: false });
  readonly loadError = signal('');
  // the roles read for this opening of the wizard, a role change shows once it is opened again
  private roleCache: RoleCache = new Map();

  readonly input = computed<WizardInput>(() => {
    const backend =
      this.backendMode() === 'existing'
        ? { existingUuid: this.existingBackendUuid() }
        : { create: this.newBackend() };
    return { vs: this.vs(), backend };
  });

  readonly problems = computed(() => {
    const taken = {
      vs: new Set(this.controllerVs()),
      backend: new Set(this.backendList().map((backend) => backend.name)),
    };
    const problems = fieldProblems(this.input(), taken);
    if (this.vipPick() !== newVipChoice) {
      return problems;
    }
    const vipNames = new Set(this.vips().map((vip) => vip.name));
    return [...vipProblems(this.newVip(), vipNames), ...problems];
  });
  private chosenVipWay = computed(() =>
    this.vipWays().find((way) => vipWayOf(way) === this.newVip().way),
  );
  // a new VIP needs its way open to the user, a draft may hold one closed since, like the ipam turned off
  private isVipWayOpen = computed(() => {
    const isNewVip = this.vipPick() === newVipChoice;
    return !isNewVip || this.chosenVipWay()?.greyed === '';
  });
  readonly vipDone = computed(() => {
    const hasProblem = this.problems().some((p) => p.field.startsWith('vip.'));
    const isAllowed = this.vipDenial() === '' && !this.checkingVip();
    return !hasProblem && isAllowed && this.isVipWayOpen();
  });
  readonly vsDone = computed(() => !this.problems().some((p) => p.field.startsWith('vs.')));
  readonly backendDone = computed(
    () => !this.problems().some((p) => p.field.startsWith('backend.')),
  );
  readonly runDone = computed(() => this.vipDone() && this.vsDone() && this.backendDone());
  readonly fileGreyed = computed(() => {
    const isIpamVip = this.vipPick() === newVipChoice && this.newVip().way === 'ipam';
    return isIpamVip ? ipamFileReason : '';
  });
  // effectiveOutput is the output the review shows, the file can't be chosen while it is greyed
  readonly effectiveOutput = computed<Output>(() =>
    this.fileGreyed() !== '' ? 'create' : this.output(),
  );

  // built holds the yaml of a new file with the documents of this run. A message the wizard can't write as JSON,
  // such as an Any of an unknown type read from the controller, ends in error, which the review shows.
  private built = computed(() => {
    if (!this.runDone() || this.fileGreyed() !== '') {
      return { text: '', error: '' };
    }
    try {
      const built = buildDocuments(this.input(), this.fileRefs());
      const docs: { kind: DocKind; message: Message }[] = [];
      if (built.backend) {
        docs.push({ kind: 'backend', message: built.backend });
      }
      docs.push({ kind: 'vs', message: built.vs });
      return { text: newFile(docs, this.vipDocument()), error: '' };
    } catch (err) {
      return { text: '', error: errorMessage(err) };
    }
  });
  readonly fileText = computed(() => this.built().text);
  readonly buildError = computed(() => this.built().error);

  private started?: Promise<void>;

  // start begins a new run each time the wizard opens. It finds out where the page runs the first time, and reads the
  // controller each time so the names and the VIPs are fresh.
  async start() {
    this.clearRun();
    this.roleCache = new Map();
    this.started ??= this.detectMode();
    await this.started;
    await this.loadController();
  }

  // the same reason the Create button of the page of the kind shows
  createGreyed(kind: KindName): string {
    const isAdmin = this.session.user()?.isAdmin ?? false;
    return adminReason(kind, 'create', this.features(), isAdmin);
  }

  // The create* methods open the creation panel of a resource the run links, above the wizard, and pick it once created
  createCertificate() {
    this.creations.start({
      kind: 'certificate',
      created: (resource) => {
        const certificate = resource as Certificate;
        this.certificateList.update((certificates) => [...certificates, certificate]);
        this.vs.update((vs) => ({ ...vs, certificateUuid: certificate.UUID }));
      },
    });
  }

  createInstance() {
    this.creations.start({
      kind: 'instance',
      created: (resource) => {
        const instance = resource as Instance;
        this.instances.update((instances) => [...instances, instance]);
        this.newVip.update((vip) => ({ ...vip, instanceUuid: instance.UUID }));
      },
    });
  }

  createZone() {
    this.creations.start({
      kind: 'zone',
      created: (resource) => {
        const zone = resource as Zone;
        this.zones.update((zones) => [...zones, zone]);
        this.newVip.update((vip) => ({ ...vip, zoneUuid: zone.UUID }));
      },
    });
  }

  // pickVip takes the choice of the VIP step. A new VIP no way is open to can't be picked.
  async pickVip(choice: string) {
    this.vipPick.set(choice);
    this.vipDenial.set('');
    // a check still running for an earlier pick no longer holds the step
    this.checkingVip.set(false);
    const isClosedNewVip = choice === newVipChoice && this.newVipGreyed() !== '';
    if (isClosedNewVip) {
      this.vipPick.set('');
      return;
    }
    await this.checkPickedVip();
  }

  // checkPickedVip checks the writer role on the picked VIP, the run changes it, and tells whether the run may go on.
  // The answer is kept for this opening of the wizard, checking the same VIP again asks nothing.
  async checkPickedVip(): Promise<boolean> {
    const choice = this.vipPick();
    if (choice === '' || choice === newVipChoice) {
      return true;
    }
    const vip = this.selectableVips().find((candidate) => candidate.UUID === choice);
    if (!vip) {
      this.vipPick.set('');
      return false;
    }
    this.checkingVip.set(true);
    let denial: string;
    try {
      denial = (await this.access.check(vip, Role.WRITER, this.roleCache))?.reason ?? '';
    } catch (err) {
      denial = `The roles on ${vip.name} can't be read: ${errorMessage(err)}`;
    }
    // a later pick wins over the answer of an earlier one
    if (this.vipPick() !== choice) {
      return false;
    }
    this.vipDenial.set(denial);
    this.checkingVip.set(false);
    return denial === '';
  }

  // setProtocol moves a port left at the default of the old protocol to the default of the new one. It also drops
  // what the backend step hides for the new protocol, a value left behind would end up in the resources unseen.
  setProtocol(protocol: Protocol) {
    const vs = this.vs();
    const keepsPort = vs.port !== defaultPorts[vs.protocol];
    const port = keepsPort ? vs.port : defaultPorts[protocol];
    this.vs.set({ ...vs, protocol, port });

    const backend = this.newBackend();
    const isL4 = protocol === 'tcp' || protocol === 'udp';
    const dropsHttpCheck = protocol === 'udp' && backend.healthCheck.type === 'http';
    const type = dropsHttpCheck ? 'tcp' : backend.healthCheck.type;
    this.newBackend.set({
      ...backend,
      tls: isL4 ? false : backend.tls,
      healthCheck: { ...backend.healthCheck, type },
    });
  }

  problemOf(field: string): string {
    return this.problems().find((problem) => problem.field === field)?.message ?? '';
  }

  // creationLabels name what createAll sends, in the order it sends it
  readonly creationLabels = computed(() =>
    creationActions(this.api, this.creationPlan()).map((action) => action.label),
  );

  // createAll creates the resources of the run on the controller, the review shows the outcome. The picked VIP is
  // checked again, it may have been locked or taken away since the wizard listed it.
  async createAll() {
    const actions = creationActions(this.api, this.creationPlan());
    const mayChangeVip = await this.checkPickedVip();
    if (!mayChangeVip) {
      const error = this.vipDenial() || 'The picked VIP is no longer offered, pick it again.';
      this.outcome.set({ created: [], notCreated: actions.map((action) => action.label), error });
      return;
    }
    this.outcome.set(await runActions(actions));
  }

  private creationPlan(): CreationPlan {
    const input = this.input();
    const backendUuid = 'existingUuid' in input.backend ? input.backend.existingUuid : '';
    return {
      input,
      backendUuid,
      certificateUuid: input.vs.certificateUuid,
      vip: this.vipPlan(),
    };
  }

  // fileRefs name the backend and the certificate of the run, flock apply finds them by name
  private fileRefs(): Refs {
    const input = this.input();
    const backend = input.backend;
    const backendName =
      'create' in backend
        ? backend.create.name.trim()
        : (this.backendList().find((b) => b.UUID === backend.existingUuid)?.name ?? '');
    const certificateUuid = input.vs.certificateUuid;
    const certificate = this.certificateList().find((c) => c.UUID === certificateUuid);
    return {
      backend: `name:${backendName}`,
      certificate: certificate ? `name:${certificate.name}` : '',
    };
  }

  // saved records a save by the cli, the review then shows how to apply the file
  saved(path: string) {
    this.savedPath.set(path);
  }

  // reset starts a new run once the review is done, the names it checks against must include what the run created
  reset() {
    this.clearRun();
    void this.loadController();
  }

  private clearRun() {
    this.vipPick.set('');
    this.vipDenial.set('');
    this.newVip.set({ ...emptyVip(), way: this.newVip().way });
    this.vs.set(emptyVs());
    this.backendMode.set('create');
    this.existingBackendUuid.set('');
    this.newBackend.set(emptyBackend());
    this.output.set('create');
    this.savedPath.set('');
    this.outcome.set(null);
    this.step.set(steps.vip);
  }

  private vipPlan(): VipPlan {
    const picked = this.pickedVip();
    if (picked) {
      return { kind: 'existing', vip: picked };
    }
    if (this.vipPick() !== newVipChoice) {
      return { kind: 'none' };
    }
    const vip = this.newVip();
    // vipDone keeps the review closed until the way is open, the run never falls back to another way
    const way = this.chosenVipWay();
    if (!way) {
      throw new Error(`No way to create the VIP ${vip.name} is offered.`);
    }
    if (way.request) {
      return { kind: 'ipam', vip, register: way.request };
    }
    return { kind: 'explicit', vip };
  }

  // vipDocument is the VIP the file lists the virtual server in, a new one holds its explicit ip
  private vipDocument(): VIP | undefined {
    const picked = this.pickedVip();
    if (picked) {
      return picked;
    }
    if (this.vipPick() !== newVipChoice) {
      return undefined;
    }
    const { name, ip } = this.newVip();
    return create(VIPSchema, { name: name.trim(), ip: ip.trim() });
  }

  private async detectMode() {
    const info = await this.cli.info();
    if (info) {
      this.cwd.set(info.cwd);
      this.mode.set('cli');
      return;
    }
    this.mode.set('controller');
  }

  // Names are checked against every virtual server and backend, listed once per opening of the wizard, since the
  // backend step offers the backends of the controller anyway and a run is short.
  private async loadController() {
    this.loadError.set('');
    try {
      const [vips, certificates, backends, vs, instances, zones, features] = await Promise.all([
        listAll((limit, offset) =>
          this.api.vip.listVIP({ limit, offset }).then((resp) => resp.vip),
        ),
        listAll((limit, offset) =>
          this.api.certificate.listCertificate({ limit, offset }).then((resp) => resp.certificates),
        ),
        listAll((limit, offset) =>
          this.api.backend.listBackend({ limit, offset }).then((resp) => resp.backends),
        ),
        listAll((limit, offset) => this.api.vs.listVS({ limit, offset }).then((resp) => resp.vs)),
        listAll((limit, offset) =>
          this.api.instance.listInstance({ limit, offset }).then((resp) => resp.instances),
        ),
        listAll((limit, offset) =>
          this.api.zone.listZone({ limit, offset }).then((resp) => resp.zones),
        ),
        this.access.features(),
      ]);
      this.vips.set(vips);
      this.certificateList.set(certificates);
      this.backendList.set(backends);
      this.controllerVs.set(vs.map((one) => one.name));
      // RegisterVip refuses a locked instance, the VIP would change it
      this.instances.set(instances.filter((instance) => !isLocked(instance)));
      this.zones.set(zones);
      this.features.set(features);
      this.setVipWays(features.ipamEnabled);
    } catch (err) {
      this.loadError.set(errorMessage(err));
    }
  }

  // setVipWays offers the create ways of the VIPs page. With the ipam off that page has no choice, the wizard then
  // offers the explicit ip alone, greyed for a user who is not an admin.
  private setVipWays(ipamEnabled: boolean) {
    const isAdmin = this.session.user()?.isAdmin ?? false;
    const features = { ipamEnabled };
    const rules = kindRules('vip', { api: this.api, dialog: this.dialog, isAdmin, features });
    const explicitOnly: CreateWay = {
      label: 'Explicit IP',
      greyed: adminReason('vip', 'create', features, isAdmin),
      request: null,
    };
    const ways = rules.createWays ?? [explicitOnly];
    this.vipWays.set(ways);
    const current = this.newVip().way;
    const isCurrentOpen = ways.some((way) => vipWayOf(way) === current && way.greyed === '');
    const firstOpen = ways.find((way) => way.greyed === '');
    if (!isCurrentOpen && firstOpen) {
      this.newVip.update((vip) => ({ ...vip, way: vipWayOf(firstOpen) }));
    }
  }
}
