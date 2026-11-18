import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { provideRouter } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { AnySchema } from '@bufbuild/protobuf/wkt';
import { ConnectError } from '@connectrpc/connect';
import { of } from 'rxjs';
import { parseAllDocuments } from 'yaml';
import { BackendSchema, type Backend } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { CertificateSchema } from '../../gen/flock/api/config/cert/v1/cert_pb';
import { InstanceSchema } from '../../gen/flock/api/config/instance/v1/instance_pb';
import {
  MemberPermissionSchema,
  MemberType,
  Role,
} from '../../gen/flock/api/config/permission/v1/permission_pb';
import { VIPSchema, type VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { ZoneSchema } from '../../gen/flock/api/config/zone/v1/zone_pb';
import type {
  HttpRoutesConfig,
  VirtualServer,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api } from '../core/api';
import { CliHost, WriteResult } from '../core/cli-host';
import { InstanceHealth } from '../core/instance-health';
import { Instances } from '../core/instances';
import { Oidc } from '../core/oidc';
import { ResourceCreations } from '../core/resource-creations';
import { Session } from '../core/session';
import { Theme } from '../core/theme';
import { Shell } from '../shell/shell';
import { lockReason } from '../edit/lock';
import { Builder } from './builder';
import { isProposedName } from '../core/names';
import { BuilderState, newVipChoice, steps } from './builder-state';

const vip = create(VIPSchema, {
  UUID: 'vip-1',
  name: 'web',
  ip: '10.0.0.1',
  virtualServersUuid: ['vs-0'],
});
const lockedVip = create(VIPSchema, {
  UUID: 'vip-locked',
  name: 'auto',
  ip: '10.0.0.9',
  metadata: { 'flock.autoprovision': 'true' },
});

// calls records the rpcs that write, in the order they ran
let calls: string[];
let user: { username: string; groups: string[]; isAdmin: boolean };
let listMemberPermissions: ReturnType<typeof vi.fn>;
let ipamEnabled: boolean;

function membership(role: Role) {
  return create(MemberPermissionSchema, {
    member: { memberName: 'alice', memberType: MemberType.USER },
    role,
  });
}

function makeApi() {
  return {
    vip: {
      listVIP: vi.fn(async () => ({ vip: [vip, lockedVip] })),
      readVIP: vi.fn(async (req: { UUID: string }) => {
        const registered = create(VIPSchema, { UUID: req.UUID, name: 'public', ip: '10.1.0.1' });
        return { vip: req.UUID === vip.UUID ? vip : registered };
      }),
      updateVIP: vi.fn(async (req: { UUID: string; vip: VIP }) => {
        calls.push('updateVIP');
        return { vip: req.vip };
      }),
      createVIP: vi.fn(async (req: { vip: VIP }) => {
        calls.push('createVIP');
        return { vip: { ...req.vip, UUID: 'vip-new' } };
      }),
      registerVip: vi.fn(async (req: { name: string }) => {
        calls.push('registerVip');
        return { vip: create(VIPSchema, { UUID: 'vip-new', name: req.name, ip: '10.1.0.1' }) };
      }),
    },
    certificate: { listCertificate: async () => ({ certificates: [] }) },
    backend: {
      listBackend: async () => ({
        backends: [create(BackendSchema, { UUID: 'backend-shared', name: 'shared' })],
      }),
      createBackend: vi.fn(async (req: { backend: { name: string } }) => {
        calls.push('createBackend');
        return { backend: { ...req.backend, UUID: 'backend-1' } };
      }),
    },
    vs: {
      listVS: async () => ({ vs: [] }),
      createVS: vi.fn(async (req: { vs: VirtualServer }) => {
        calls.push('createVS');
        return { vs: { ...req.vs, UUID: 'vs-1' } };
      }),
    },
    instance: {
      listInstance: async () => ({
        instances: [create(InstanceSchema, { UUID: 'instance-1', name: 'edge' })],
      }),
      readInstance: async () => ({
        instance: create(InstanceSchema, { UUID: 'instance-1', name: 'edge' }),
      }),
    },
    zone: { listZone: async () => ({ zones: [] }) },
    list: vi.fn(async () => []),
    info: { getControllerFeatures: async () => ({ ipamEnabled }) },
    permission: { listMemberPermissions },
  };
}

let api: ReturnType<typeof makeApi>;

interface FakeCli {
  info: () => Promise<{ cwd: string } | null>;
  standaloneInfo: () => Promise<{ controller: string } | null>;
  writeFile: ReturnType<typeof vi.fn>;
}

// dialogAnswers holds what the next confirmations close with
let dialogAnswers: boolean[];
let cli: FakeCli;

function configure(underCli: boolean, options: { realSession?: boolean } = {}) {
  cli = {
    info: async () => (underCli ? { cwd: '/work' } : null),
    standaloneInfo: async () => null,
    writeFile: vi.fn(async (): Promise<WriteResult> => ({ ok: true })),
  };
  const session = options.realSession ? [] : [{ provide: Session, useValue: { user: () => user } }];
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      { provide: Api, useValue: api },
      { provide: CliHost, useValue: cli },
      {
        provide: MatDialog,
        useValue: { open: () => ({ afterClosed: () => of(dialogAnswers.shift() ?? false) }) },
      },
      ...session,
    ],
  });
}

async function settle(fixture: ComponentFixture<unknown>) {
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
}

// button finds a button by its label, the ligature of its icon left out
function button(root: Element, text: string): HTMLButtonElement {
  const labelOf = (candidate: HTMLButtonElement) => {
    const copy = candidate.cloneNode(true) as HTMLElement;
    copy.querySelectorAll('mat-icon').forEach((icon) => icon.remove());
    return copy.textContent?.trim();
  };
  const found = [...root.querySelectorAll('button')].find(
    (candidate) => labelOf(candidate) === text,
  );
  if (!found) {
    throw new Error(`no button ${text}`);
  }
  return found;
}

function step(root: Element, selector: string): Element {
  const found = root.querySelector(selector);
  if (!found) {
    throw new Error(`no step ${selector}`);
  }
  return found;
}

function type(root: Element, name: string, value: string) {
  const input = root.querySelector(`input[name="${name}"]`) as HTMLInputElement;
  input.value = value;
  input.dispatchEvent(new Event('input'));
}

async function render(): Promise<ComponentFixture<Builder>> {
  const fixture = TestBed.createComponent(Builder);
  await settle(fixture);
  return fixture;
}

// fillRun goes through the steps from the VIP step to the review with the given VIP choice and a new backend
async function fillRun(fixture: ComponentFixture<Builder>, vipChoice = vip.UUID) {
  const page = fixture.nativeElement as HTMLElement;
  await TestBed.inject(BuilderState).pickVip(vipChoice);
  await settle(fixture);
  button(step(page, 'flock-vip-step'), 'Next').click();
  await settle(fixture);

  type(step(page, 'flock-vs-step'), 'vsName', 'shop');
  await settle(fixture);
  button(step(page, 'flock-vs-step'), 'Next').click();
  await settle(fixture);

  const backendStep = step(page, 'flock-backend-step');
  type(backendStep, 'backendName', 'shop');
  type(backendStep, 'ip0', '10.0.0.10');
  type(backendStep, 'port0', '8080');
  await settle(fixture);
  button(backendStep, 'Next').click();
  await settle(fixture);
}

// the options of the select opened last, the panel of a select closed before may still be in the document
function openOptions(): HTMLElement[] {
  const panels = document.querySelectorAll('.mat-mdc-select-panel');
  return [...(panels[panels.length - 1]?.querySelectorAll<HTMLElement>('mat-option') ?? [])];
}

async function openSelect(fixture: ComponentFixture<Builder>, name: string) {
  const page = fixture.nativeElement as HTMLElement;
  (page.querySelector(`mat-select[name="${name}"] .mat-mdc-select-trigger`) as HTMLElement).click();
  await settle(fixture);
}

function review(fixture: ComponentFixture<Builder>): Element {
  return step(fixture.nativeElement, 'flock-review-step');
}

async function chooseOutput(fixture: ComponentFixture<Builder>, label: string) {
  button(review(fixture), label).click();
  await settle(fixture);
}

// backendOfCreatedVs is the backend the route of the http virtual server sent to createVS goes to
function backendOfCreatedVs(): string {
  const routes = api.vs.createVS.mock.calls[0][0].vs.routeConfig.value as HttpRoutesConfig;
  return String(routes.virtualHosts[0].routes[0].action.value);
}

function kindsOf(text: string): string[] {
  return parseAllDocuments(text).map((doc) => doc.toJS().kind);
}

function listed(root: Element, title: string): string[] {
  const section = [...root.querySelectorAll('.outcome section')].find((candidate) =>
    candidate.querySelector('h3')?.textContent?.includes(title),
  );
  return [...(section?.querySelectorAll('li') ?? [])].map((item) => item.textContent!.trim());
}

describe('Builder', () => {
  beforeEach(() => {
    localStorage.clear();
    dialogAnswers = [];
    calls = [];
    user = { username: 'alice', groups: [], isAdmin: false };
    listMemberPermissions = vi.fn(async () => ({
      memberPermissions: [membership(Role.WRITER)],
    }));
    ipamEnabled = true;
    api = makeApi();
  });

  describe('in the shell', () => {
    beforeEach(() => {
      configure(false, { realSession: true });
      localStorage.setItem('session_kind', 'flock');
      localStorage.setItem('username', 'alice');
      TestBed.overrideProvider(Oidc, { useValue: {} });
      TestBed.overrideProvider(Theme, { useValue: { isDark: () => false } });
      TestBed.overrideProvider(Instances, {
        useValue: { load: () => undefined, current: () => null },
      });
      TestBed.overrideProvider(InstanceHealth, {
        useValue: { degraded: () => [], degradedLevel: () => '' },
      });
    });

    it('opens the wizard from the top bar and starts a new run at each opening', async () => {
      const fixture = TestBed.createComponent(Shell);
      await settle(fixture);
      const toolbar = (fixture.nativeElement as HTMLElement).querySelector('mat-toolbar')!;

      (toolbar.querySelector('button[aria-label="Expose a service"]') as HTMLButtonElement).click();
      await settle(fixture);
      await settle(fixture);
      const panel = document.querySelector('flock-side-panel')!;
      expect(panel.querySelector('.title')?.textContent).toContain('Expose a service');
      button(step(panel, 'flock-vip-step'), 'Next').click();
      await settle(fixture);
      TestBed.inject(BuilderState).vs.update((vs) => ({ ...vs, name: 'shop' }));

      button(panel, 'Ok').click();
      await new Promise((resolve) => setTimeout(resolve, 350));
      await fixture.whenStable();
      expect(document.querySelector('flock-side-panel')).toBeNull();

      (toolbar.querySelector('button[aria-label="Expose a service"]') as HTMLButtonElement).click();
      await settle(fixture);
      await settle(fixture);
      expect(document.querySelector('flock-side-panel flock-vip-step')).not.toBeNull();
      expect(isProposedName(TestBed.inject(BuilderState).vs().name)).toBe(true);
      fixture.destroy();
    });
  });

  describe('on a controller', () => {
    beforeEach(() => configure(false));

    it('has the steps VIP, Virtual server, Backend and Review', async () => {
      const fixture = await render();

      const labels = [
        ...(fixture.nativeElement as HTMLElement).querySelectorAll(
          'mat-step-header .mat-step-label',
        ),
      ].map((label) => label.textContent?.trim());
      expect(labels).toEqual(['VIP', 'Virtual server', 'Backend', 'Review']);
      expect(document.querySelector('flock-file-step')).toBeNull();
    });

    it('lists the VIPs but the locked ones', async () => {
      const fixture = await render();
      const vipStep = step(fixture.nativeElement, 'flock-vip-step');

      (vipStep.querySelector('.mat-mdc-select-trigger') as HTMLElement).click();
      await settle(fixture);

      const options = [...document.querySelectorAll('mat-option')].map((option) =>
        option.textContent?.trim(),
      );
      expect(options).toContain('web (10.0.0.1)');
      expect(options.some((option) => option?.includes('auto'))).toBe(false);
    });

    it('checks the writer role on a picked VIP once and shows the denial on the step', async () => {
      const reason = 'You are a reader of web. Editing needs the writer role, ask an owner.';
      listMemberPermissions.mockResolvedValue({ memberPermissions: [membership(Role.READER)] });
      const fixture = await render();
      const vipStep = step(fixture.nativeElement, 'flock-vip-step');

      (vipStep.querySelector('.mat-mdc-select-trigger') as HTMLElement).click();
      await settle(fixture);
      const option = [...document.querySelectorAll<HTMLElement>('mat-option')].find((candidate) =>
        candidate.textContent?.includes('web (10.0.0.1)'),
      );
      option!.click();
      await settle(fixture);

      expect(listMemberPermissions).toHaveBeenCalledTimes(1);
      expect(listMemberPermissions.mock.calls[0][0]).toMatchObject({ resourceUuid: 'vip-1' });
      expect(vipStep.querySelector('[role="alert"]')?.textContent).toContain(reason);
      expect(button(vipStep, 'Next').disabled).toBe(true);

      await TestBed.inject(BuilderState).pickVip(vip.UUID);
      expect(listMemberPermissions).toHaveBeenCalledTimes(1);
    });

    it('frees the step when another pick replaces a VIP still being checked', async () => {
      listMemberPermissions.mockReturnValue(new Promise(() => undefined));
      await render();
      const state = TestBed.inject(BuilderState);

      void state.pickVip(vip.UUID);
      expect(state.vipDone()).toBe(false);
      await state.pickVip('');

      expect(state.vipDone()).toBe(true);
    });

    it('offers Any domain (*) on an http virtual server only, and names the * chip any domain', async () => {
      const fixture = await render();
      const page = fixture.nativeElement as HTMLElement;
      await TestBed.inject(BuilderState).pickVip(vip.UUID);
      await settle(fixture);
      button(step(page, 'flock-vip-step'), 'Next').click();
      await settle(fixture);
      const vsStep = step(page, 'flock-vs-step');

      button(vsStep, 'Any domain (*)').click();
      await settle(fixture);
      expect(TestBed.inject(BuilderState).vs().domains).toEqual(['*']);
      expect(button(vsStep, 'Any domain (*)').disabled).toBe(true);
      expect(vsStep.querySelector('mat-chip-row')!.textContent).toContain('any domain');

      for (const protocol of ['https', 'tls', 'tcp']) {
        vsStep.querySelector<HTMLElement>(`[data-choice="${protocol}"]`)!.click();
        await settle(fixture);
        expect(vsStep.querySelector('button.any-domain')).toBeNull();
      }
    });

    it('creates a dns backend picked from its card, with only the dns fields asked', async () => {
      const fixture = await render();
      const page = fixture.nativeElement as HTMLElement;
      await TestBed.inject(BuilderState).pickVip(vip.UUID);
      await settle(fixture);
      button(step(page, 'flock-vip-step'), 'Next').click();
      await settle(fixture);
      type(step(page, 'flock-vs-step'), 'vsName', 'shop');
      await settle(fixture);
      button(step(page, 'flock-vs-step'), 'Next').click();
      await settle(fixture);

      const backendStep = step(page, 'flock-backend-step');
      type(backendStep, 'backendName', 'shop');
      backendStep.querySelector<HTMLButtonElement>('[data-choice="dns"]')!.click();
      await settle(fixture);
      expect(backendStep.querySelector('input[name="ip0"]')).toBeNull();
      expect(button(backendStep, 'Next').disabled).toBe(true);
      type(backendStep, 'dnsHost', 'shop.internal');
      type(backendStep, 'dnsPort', '8080');
      await settle(fixture);
      button(backendStep, 'Next').click();
      await settle(fixture);
      await chooseOutput(fixture, 'Create the resources');
      button(review(fixture), 'Create').click();
      await settle(fixture);

      const createBackend = TestBed.inject(Api).backend.createBackend as ReturnType<typeof vi.fn>;
      const sent = createBackend.mock.calls[0][0] as { backend: Backend };
      expect(sent.backend.endpoint).toMatchObject({
        case: 'dns',
        value: { host: 'shop.internal', port: 8080 },
      });
    });

    it('asks for the eds password as a secret once the url names a user', async () => {
      const fixture = await render();
      const page = fixture.nativeElement as HTMLElement;
      await TestBed.inject(BuilderState).pickVip(vip.UUID);
      await settle(fixture);
      button(step(page, 'flock-vip-step'), 'Next').click();
      await settle(fixture);
      type(step(page, 'flock-vs-step'), 'vsName', 'shop');
      await settle(fixture);
      button(step(page, 'flock-vs-step'), 'Next').click();
      await settle(fixture);

      const backendStep = step(page, 'flock-backend-step');
      backendStep.querySelector<HTMLButtonElement>('[data-choice="eds"]')!.click();
      await settle(fixture);
      type(backendStep, 'edsUrl', 'grpc://vigie:8080');
      await settle(fixture);
      expect(backendStep.querySelector('flock-secret-ref-picker')).toBeNull();

      type(backendStep, 'edsUrl', 'grpc://lb@vigie:8080');
      await settle(fixture);
      expect(backendStep.querySelector('flock-secret-ref-picker')).not.toBeNull();
    });

    it('creates the backend, the virtual server and adds it to the VIP, with the uuids chained', async () => {
      const fixture = await render();
      await fillRun(fixture);

      await chooseOutput(fixture, 'Create the resources');
      button(review(fixture), 'Create').click();
      await settle(fixture);

      expect(calls).toEqual(['createBackend', 'createVS', 'updateVIP']);
      expect(backendOfCreatedVs()).toBe('backend-1');
      const updated = api.vip.updateVIP.mock.calls[0][0];
      expect(updated.UUID).toBe('vip-1');
      expect(updated.vip.virtualServersUuid).toEqual(['vs-0', 'vs-1']);
      expect(listed(review(fixture), 'Created')).toEqual([
        'backend shop',
        'virtual server shop',
        'VIP web (virtual server added)',
      ]);
    });

    it('stops at the first error and lists what was created and what was not', async () => {
      api.vs.createVS.mockRejectedValue(new ConnectError('port 80 is taken'));
      const fixture = await render();
      await fillRun(fixture);

      await chooseOutput(fixture, 'Create the resources');
      button(review(fixture), 'Create').click();
      await settle(fixture);

      expect(calls).toEqual(['createBackend']);
      expect(api.vip.updateVIP).not.toHaveBeenCalled();
      expect(listed(review(fixture), 'Created:')).toEqual(['backend shop']);
      expect(listed(review(fixture), 'Not created:')).toEqual([
        'virtual server shop',
        'VIP web (virtual server added)',
      ]);
      expect(review(fixture).querySelector('[role="alert"]')?.textContent).toContain(
        'port 80 is taken',
      );
    });

    it('refers to an existing backend by its uuid', async () => {
      const fixture = await render();
      const state = TestBed.inject(BuilderState);
      await fillRun(fixture);
      state.backendMode.set('existing');
      state.existingBackendUuid.set('backend-shared');
      await settle(fixture);

      await chooseOutput(fixture, 'Create the resources');
      button(review(fixture), 'Create').click();
      await settle(fixture);

      expect(calls).toEqual(['createVS', 'updateVIP']);
      expect(backendOfCreatedVs()).toBe('backend-shared');
    });

    it('registers a new VIP from the ipam then adds the virtual server to it', async () => {
      const fixture = await render();
      const state = TestBed.inject(BuilderState);
      state.newVip.update((newVip) => ({
        ...newVip,
        name: 'public',
        way: 'ipam',
        target: 'instance',
        instanceUuid: 'instance-1',
      }));
      await fillRun(fixture, newVipChoice);

      const fileWay = [...review(fixture).querySelectorAll('mat-button-toggle')].find((toggle) =>
        toggle.textContent?.includes('Generate a file'),
      );
      expect(fileWay?.querySelector('button')?.getAttribute('aria-disabled')).toBe('true');
      await chooseOutput(fixture, 'Create the resources');
      button(review(fixture), 'Create').click();
      await settle(fixture);

      expect(calls).toEqual(['createBackend', 'createVS', 'registerVip', 'updateVIP']);
      expect(api.vip.registerVip.mock.calls[0][0]).toMatchObject({
        name: 'public',
        location: { case: 'instanceUuid', value: 'instance-1' },
      });
      expect(api.vip.updateVIP.mock.calls[0][0]).toMatchObject({
        UUID: 'vip-new',
        vip: { virtualServersUuid: ['vs-1'] },
      });
    });

    it('creates an instance from the instance select and picks it', async () => {
      const fixture = await render();
      const state = TestBed.inject(BuilderState);
      state.newVip.update((newVip) => ({ ...newVip, way: 'ipam', target: 'instance' }));
      await state.pickVip(newVipChoice);
      await settle(fixture);

      await openSelect(fixture, 'vipInstance');
      expect(openOptions()[0].textContent).toContain('Create an instance');
      openOptions()[0].click();
      await settle(fixture);

      const creations = TestBed.inject(ResourceCreations).open();
      expect(creations.map((creation) => creation.kind)).toEqual(['instance']);
      expect(state.newVip().instanceUuid).toBe('');
      creations[0].created(create(InstanceSchema, { UUID: 'instance-new', name: 'core' }));
      await settle(fixture);

      expect(state.newVip().instanceUuid).toBe('instance-new');
      const select = (fixture.nativeElement as HTMLElement).querySelector(
        'mat-select[name="vipInstance"]',
      );
      expect(select?.textContent).toContain('core');
    });

    it('greys the creation of a zone for a user who is not an admin', async () => {
      const fixture = await render();
      const state = TestBed.inject(BuilderState);
      state.newVip.update((newVip) => ({ ...newVip, way: 'ipam', target: 'zone' }));
      await state.pickVip(newVipChoice);
      await settle(fixture);

      await openSelect(fixture, 'vipZone');
      const createZone = openOptions()[0];
      expect(createZone.textContent).toContain('Create a zone');
      expect(createZone.getAttribute('aria-disabled')).toBe('true');
    });

    it('picks a zone an admin creates from the zone select', async () => {
      user.isAdmin = true;
      const fixture = await render();
      const state = TestBed.inject(BuilderState);
      state.newVip.update((newVip) => ({ ...newVip, way: 'ipam', target: 'zone' }));
      await state.pickVip(newVipChoice);
      await settle(fixture);

      await openSelect(fixture, 'vipZone');
      openOptions()[0].click();
      await settle(fixture);
      const creation = TestBed.inject(ResourceCreations).open()[0];
      expect(creation.kind).toBe('zone');
      creation.created(create(ZoneSchema, { UUID: 'zone-new', name: 'paris' }));
      await settle(fixture);

      expect(state.newVip().zoneUuid).toBe('zone-new');
    });

    it('creates a certificate from the certificate select of an https virtual server', async () => {
      const fixture = await render();
      const state = TestBed.inject(BuilderState);
      state.setProtocol('https');
      state.step.set(steps.vs);
      await settle(fixture);

      await openSelect(fixture, 'certificate');
      expect(openOptions()[0].textContent).toContain('Create a certificate');
      openOptions()[0].click();
      await settle(fixture);
      const creation = TestBed.inject(ResourceCreations).open()[0];
      expect(creation.kind).toBe('certificate');
      creation.created(create(CertificateSchema, { UUID: 'cert-new', name: 'shop-tls' }));
      await settle(fixture);

      expect(state.vs().certificateUuid).toBe('cert-new');
      const select = (fixture.nativeElement as HTMLElement).querySelector(
        'mat-select[name="certificate"]',
      );
      expect(select?.textContent).toContain('shop-tls');
    });

    it('creates a new VIP with an explicit ip holding the virtual server, for an admin', async () => {
      user.isAdmin = true;
      const fixture = await render();
      const state = TestBed.inject(BuilderState);
      state.newVip.update((newVip) => ({
        ...newVip,
        name: 'public',
        way: 'explicit',
        ip: '192.0.2.10',
      }));
      await fillRun(fixture, newVipChoice);

      await chooseOutput(fixture, 'Create the resources');
      button(review(fixture), 'Create').click();
      await settle(fixture);

      expect(calls).toEqual(['createBackend', 'createVS', 'createVIP']);
      expect(api.vip.createVIP.mock.calls[0][0].vip).toMatchObject({
        name: 'public',
        ip: '192.0.2.10',
        virtualServersUuid: ['vs-1'],
      });
    });

    it('greys the explicit ip for a user who is not an admin', async () => {
      const fixture = await render();
      await TestBed.inject(BuilderState).pickVip(newVipChoice);
      await settle(fixture);

      const explicit = [
        ...step(fixture.nativeElement, 'flock-vip-step').querySelectorAll('mat-button-toggle'),
      ].find((toggle) => toggle.textContent?.includes('Explicit IP'));
      expect(explicit?.querySelector('button')?.getAttribute('aria-disabled')).toBe('true');
      expect(TestBed.inject(BuilderState).newVip().way).toBe('ipam');
    });

    it('offers no new VIP to a user who is not an admin while the ipam is off', async () => {
      ipamEnabled = false;
      const fixture = await render();
      const vipStep = step(fixture.nativeElement, 'flock-vip-step');

      (vipStep.querySelector('.mat-mdc-select-trigger') as HTMLElement).click();
      await settle(fixture);

      const newOption = [...document.querySelectorAll<HTMLElement>('mat-option')].find(
        (candidate) => candidate.textContent?.includes('New VIP'),
      );
      expect(newOption?.getAttribute('aria-disabled')).toBe('true');
      expect(newOption?.textContent).toContain('Only an admin of the controller can create a VIP.');
    });

    it('creates and writes the backend picked among two sharing its name', async () => {
      api.backend.listBackend = async () => ({
        backends: [
          create(BackendSchema, { UUID: 'backend-a', name: 'shared' }),
          create(BackendSchema, { UUID: 'backend-b', name: 'shared' }),
        ],
      });
      const fixture = await render();
      const state = TestBed.inject(BuilderState);
      await fillRun(fixture);
      state.backendMode.set('existing');
      await settle(fixture);
      state.step.set(steps.backend);
      await settle(fixture);
      (
        step(fixture.nativeElement, 'flock-backend-step').querySelector(
          '.mat-mdc-select-trigger',
        ) as HTMLElement
      ).click();
      await settle(fixture);
      const options = [...document.querySelectorAll<HTMLElement>('mat-option')];
      expect(options.map((option) => option.textContent?.trim())).toEqual([
        'shared backend-a',
        'shared backend-b',
      ]);
      options[1].click();
      await settle(fixture);
      state.step.set(steps.review);
      await settle(fixture);

      await chooseOutput(fixture, 'Generate a file');
      expect(review(fixture).querySelector('pre.file')?.textContent).toContain(
        'backend_uuid: name:shared',
      );
      await chooseOutput(fixture, 'Create the resources');
      button(review(fixture), 'Create').click();
      await settle(fixture);

      expect(backendOfCreatedVs()).toBe('backend-b');
    });

    it('refuses to add the virtual server to a VIP locked since the wizard listed it', async () => {
      const locked = create(VIPSchema, { ...vip, metadata: { 'flock.autoprovision': 'true' } });
      api.vip.readVIP.mockResolvedValue({ vip: locked });
      const fixture = await render();
      await fillRun(fixture);

      await chooseOutput(fixture, 'Create the resources');
      button(review(fixture), 'Create').click();
      await settle(fixture);

      expect(calls).toEqual(['createBackend', 'createVS']);
      expect(listed(review(fixture), 'Not created:')).toEqual(['VIP web (virtual server added)']);
      expect(review(fixture).querySelector('[role="alert"]')?.textContent).toContain(
        `The VIP web is locked. ${lockReason}`,
      );
    });

    it('shows the creation when the file is the chosen output and a VIP from the ipam is picked', async () => {
      const fixture = await render();
      const state = TestBed.inject(BuilderState);
      await fillRun(fixture);
      await chooseOutput(fixture, 'Generate a file');

      state.newVip.update((newVip) => ({
        ...newVip,
        name: 'public',
        way: 'ipam',
        instanceUuid: 'instance-1',
      }));
      await state.pickVip(newVipChoice);
      await settle(fixture);

      expect(state.effectiveOutput()).toBe('create');
      expect(button(review(fixture), 'Create')).toBeTruthy();
    });

    it('drops a pick of a new VIP no way is open to', async () => {
      ipamEnabled = false;
      await render();
      const state = TestBed.inject(BuilderState);

      await state.pickVip(newVipChoice);

      expect(state.vipPick()).toBe('');
    });

    it('generates a new file holding a backend, a vs and the vip document', async () => {
      const fixture = await render();
      await fillRun(fixture);

      await chooseOutput(fixture, 'Generate a file');

      const text = review(fixture).querySelector('pre.file')?.textContent ?? '';
      expect(kindsOf(text)).toEqual(['backend', 'vs', 'vip']);
      expect(text).toContain('name:shop');
      expect(parseAllDocuments(text)[2].toJS().virtual_servers_uuid).toEqual(['vs-0', 'name:shop']);
      expect(button(review(fixture), 'Download')).toBeTruthy();
      expect(calls).toEqual([]);
    });

    it('downloads the file as resources.yml', async () => {
      const names: string[] = [];
      vi.stubGlobal('URL', { createObjectURL: () => 'blob:x', revokeObjectURL: () => undefined });
      const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
        this: HTMLAnchorElement,
      ) {
        names.push(this.download);
      });
      const fixture = await render();
      await fillRun(fixture);
      await chooseOutput(fixture, 'Generate a file');
      button(review(fixture), 'Download').click();
      click.mockRestore();
      vi.unstubAllGlobals();

      expect(names).toEqual(['resources.yml']);
    });

    it('warns when the picked vip name is shared by several vips', async () => {
      const twin = create(VIPSchema, { UUID: 'vip-2', name: 'web', ip: '10.0.0.2' });
      api.vip.listVIP.mockResolvedValue({ vip: [vip, twin] });
      const fixture = await render();

      await TestBed.inject(BuilderState).pickVip(vip.UUID);
      await settle(fixture);

      expect(step(fixture.nativeElement, 'flock-vip-step').textContent).toContain(
        '2 VIPs are named web',
      );
    });

    it('shows on the review why the documents could not be written', async () => {
      const unknown = create(AnySchema, { typeUrl: 'type.googleapis.com/unknown.Announce' });
      api.vip.listVIP.mockResolvedValue({
        vip: [create(VIPSchema, { ...vip, typedAnnounce: unknown })],
      });
      const fixture = await render();
      await fillRun(fixture);

      await chooseOutput(fixture, 'Generate a file');

      expect(review(fixture).querySelector('pre.file')).toBeNull();
      expect(review(fixture).querySelector('[role="alert"]')?.textContent).toContain(
        "The documents can't be written",
      );
    });

    it('refuses a name the controller already uses', async () => {
      const fixture = await render();
      const state = TestBed.inject(BuilderState);
      state.backendMode.set('create');
      state.newBackend.update((backend) => ({ ...backend, name: 'shared' }));

      expect(state.problemOf('backend.name')).toContain('already used');
      fixture.destroy();
    });
  });

  describe('under the cli', () => {
    beforeEach(() => configure(true));

    it('saves a new file and shows the apply command', async () => {
      const fixture = await render();
      await fillRun(fixture);
      await chooseOutput(fixture, 'Generate a file');

      type(review(fixture), 'savePath', 'stack.yml');
      await settle(fixture);
      button(review(fixture), 'Save').click();
      await settle(fixture);

      const [path, text, overwrite] = cli.writeFile.mock.calls[0];
      expect(path).toBe('stack.yml');
      expect(kindsOf(text)).toEqual(['backend', 'vs', 'vip']);
      expect(overwrite).toBe(false);
      expect(review(fixture).textContent).toContain('flock apply -f stack.yml');
    });

    it('asks before overwriting a file that exists', async () => {
      cli.writeFile
        .mockResolvedValueOnce({ ok: false, exists: true })
        .mockResolvedValueOnce({ ok: true });
      dialogAnswers = [true];
      const fixture = await render();
      await fillRun(fixture);
      await chooseOutput(fixture, 'Generate a file');

      button(review(fixture), 'Save').click();
      await settle(fixture);

      expect(cli.writeFile.mock.calls.map((call) => call[2])).toEqual([false, true]);
      expect(review(fixture).textContent).toContain('flock apply -f resources.yml');
    });

    it('keeps the file when the overwrite is declined', async () => {
      cli.writeFile.mockResolvedValue({ ok: false, exists: true });
      dialogAnswers = [false];
      const fixture = await render();
      await fillRun(fixture);
      await chooseOutput(fixture, 'Generate a file');

      button(review(fixture), 'Save').click();
      await settle(fixture);

      expect(cli.writeFile).toHaveBeenCalledTimes(1);
      expect(TestBed.inject(BuilderState).savedPath()).toBe('');
    });

    it('shows the problems the cli found in the file', async () => {
      const problems = [{ file: 'new.yml', index: 2, field: 'port', message: 'bad port' }];
      cli.writeFile.mockResolvedValue({ ok: false, problems });
      const fixture = await render();
      await fillRun(fixture);
      await chooseOutput(fixture, 'Generate a file');

      button(review(fixture), 'Save').click();
      await settle(fixture);

      expect(review(fixture).textContent).toContain('document 2 port: bad port');
    });
  });
});
