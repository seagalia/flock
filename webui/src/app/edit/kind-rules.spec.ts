import { Component, signal } from '@angular/core';
import { Code, ConnectError } from '@connectrpc/connect';
import { ViolationsSchema } from '../../gen/buf/validate/validate_pb';
import { RegisterVipRequestSchema } from '../../gen/flock/services/vip/v1/vip_pb';
import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { MatTooltip } from '@angular/material/tooltip';
import { By } from '@angular/platform-browser';
import { provideRouter } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { of } from 'rxjs';
import { CertificateSchema } from '../../gen/flock/api/config/cert/v1/cert_pb';
import { SecretSchema } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { InstanceSchema } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { ExternalAuthSchema } from '../../gen/flock/api/config/route/v1/external_auth_pb';
import { RouteSchema } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import {
  HttpRoutesConfig,
  VirtualServer,
  VirtualServerSchema,
} from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { IpPoolSchema, ZoneSchema } from '../../gen/flock/api/config/zone/v1/zone_pb';
import { Api } from '../core/api';
import { KindName, Resource } from '../core/resources';
import { Session } from '../core/session';
import { Access, ControllerFeatures } from './access';
import { kindRules, RulesContext } from './kind-rules';
import { lockReason } from './lock';
import { FieldsFailure, KindRules, ResourcePanel } from './resource-panel';

@Component({
  imports: [ResourcePanel],
  template: ` <flock-resource-panel [kind]="kind()" [resource]="resource()" [rules]="rules()" /> `,
})
class Host {
  kind = signal<KindName>('vip');
  resource = signal<Resource | null>(null);
  rules = signal<KindRules>({});
}

let calls: Record<string, ReturnType<typeof vi.fn>>;
let dialogs: unknown[];
let features: ControllerFeatures;
let sessionAdmin: boolean;

function fakeApi() {
  const echo = (field: string) =>
    vi.fn(async (req: Record<string, unknown>) => ({ [field]: req[field] }));
  calls = {
    readVIP: vi.fn(async () => ({ vip: create(VIPSchema, { UUID: 'v1', name: 'web-vip' }) })),
    createVIP: echo('vip'),
    updateVIP: echo('vip'),
    registerVip: vi.fn(async (req: { name: string }) => ({
      vip: create(VIPSchema, { UUID: 'v9', name: req.name, ip: '10.0.0.9' }),
    })),
    readInstance: vi.fn(async () => ({ instance: storedInstance() })),
    updateInstance: echo('instance'),
    addVipToInstance: vi.fn(async () => ({})),
    deleteVipFromInstance: vi.fn(async () => ({})),
    readZone: vi.fn(async () => ({ zone: storedZone() })),
    addPoolToZone: vi.fn(async () => ({ zone: storedZone() })),
    updatePoolOfZone: vi.fn(async () => ({ zone: storedZone() })),
    deletePoolFromZone: vi.fn(async () => ({ zone: storedZone() })),
    readCertificate: vi.fn(async () => ({ certificate: storedCertificate() })),
    updateCertificate: echo('certificate'),
  };
  return {
    vip: calls,
    instance: calls,
    zone: calls,
    certificate: calls,
    list: vi.fn(async () => []),
  } as unknown as Api;
}

function storedInstance() {
  return create(InstanceSchema, {
    UUID: 'i1',
    name: 'edge',
    zoneUuid: 'z1',
    resourceType: 'starling',
    vipsUuid: ['v1'],
  });
}

function storedZone() {
  return create(ZoneSchema, {
    UUID: 'z1',
    name: 'dc1',
    zone: 'dc1',
    pools: [
      { name: 'public', subnets: [{ ip: '10.0.0.0', prefixLength: 24 }] },
      {
        name: 'managed',
        subnets: [{ ip: '10.1.0.0', prefixLength: 24 }],
        metadata: { 'flock.autoprovision': 'true' },
      },
    ],
  });
}

function storedCertificate() {
  return create(CertificateSchema, {
    UUID: 'c1',
    name: 'web',
    certChain: '-----BEGIN CERTIFICATE-----',
    privateKey: '',
  });
}

let api: Api;

// a virtual server defining the policy sso, whose only virtual host names the policy given
function vsNaming(name: string): VirtualServer {
  return create(VirtualServerSchema, {
    name: 'web',
    routeConfig: {
      case: 'httpRoutesConfig',
      value: { externalAuths: [{ name: 'sso' }], virtualHosts: [{ externalAuth: name }] },
    },
  });
}

function context(isAdmin: boolean, ipamEnabled: boolean): RulesContext {
  return { api, dialog: TestBed.inject(MatDialog), isAdmin, features: { ipamEnabled } };
}

function setup() {
  api = fakeApi();
  dialogs = [];
  features = { ipamEnabled: true };
  sessionAdmin = false;
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      { provide: Api, useValue: api },
      { provide: Access, useValue: { check: async () => null, features: async () => features } },
      {
        provide: Session,
        useValue: { user: () => ({ username: 'alice', groups: [], isAdmin: sessionAdmin }) },
      },
      {
        provide: MatDialog,
        useValue: {
          open: (_: unknown, config: { data: unknown }) => {
            dialogs.push(config.data);
            return { afterClosed: () => of(true) };
          },
        },
      },
    ],
  });
}

async function settle(fixture: { whenStable(): Promise<unknown> }) {
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
}

async function render(kind: KindName, resource: Resource | null, rules: KindRules) {
  const fixture = TestBed.createComponent(Host);
  fixture.componentInstance.kind.set(kind);
  fixture.componentInstance.resource.set(resource);
  fixture.componentInstance.rules.set(rules);
  await settle(fixture);
  return fixture;
}

function panel(): HTMLElement {
  return document.body.querySelector('flock-side-panel')!;
}

function card(title: string): HTMLElement {
  return panel().querySelector(`[data-card="${title}"]`)!;
}

function button(root: Element, text: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
    b.textContent?.trim().endsWith(text),
  );
  if (!found) {
    throw new Error(`no button ${text}`);
  }
  return found;
}

function type(input: HTMLInputElement, text: string) {
  input.value = text;
  input.dispatchEvent(new Event('input'));
}

function tooltipOf(fixture: ReturnType<typeof TestBed.createComponent>, element: Element) {
  const debug = fixture.debugElement.query(By.css('flock-resource-panel'));
  const found = debug.queryAll(By.directive(MatTooltip)).find((d) => d.nativeElement === element);
  return found?.injector.get(MatTooltip).message;
}

describe('kindRules', () => {
  beforeEach(setup);

  afterEach(() => {
    document
      .querySelectorAll('flock-side-panel, .cdk-overlay-container')
      .forEach((e) => e.remove());
  });

  it('flags a virtual host naming an undefined policy', () => {
    const problems = kindRules('vs', context(false, false)).problems!(null, vsNaming('missing'));
    expect(problems).toEqual([
      {
        path: 'http_routes_config.virtual_hosts[0].external_auth',
        message: 'external auth missing is not defined in external_auths',
      },
    ]);
  });

  it('flags a route naming an undefined policy and a policy defined twice', () => {
    const vs = vsNaming('sso');
    const config = (vs.routeConfig as { value: HttpRoutesConfig }).value;
    config.externalAuths.push(create(ExternalAuthSchema, { name: 'sso' }));
    config.virtualHosts[0].routes = [
      create(RouteSchema, {
        name: 'open',
        externalAuth: { case: 'externalAuthDisabled', value: true },
      }),
      create(RouteSchema, {
        name: 'admin',
        externalAuth: { case: 'externalAuthName', value: 'nope' },
      }),
    ];

    expect(kindRules('vs', context(false, false)).problems!(null, vs)).toEqual([
      {
        path: 'http_routes_config.external_auths[1].name',
        message: 'external auth sso is defined twice',
      },
      {
        path: 'http_routes_config.virtual_hosts[0].routes[1].external_auth',
        message: 'external auth nope is not defined in external_auths',
      },
    ]);
  });

  it('flags an upstream header the check reads, whatever its case', () => {
    const vs = vsNaming('sso');
    const config = (vs.routeConfig as { value: HttpRoutesConfig }).value;
    config.externalAuths[0].requestHeaders = ['Cookie'];
    config.externalAuths[0].upstreamHeaders = ['x-user', 'Authorization', 'cookie'];

    expect(kindRules('vs', context(false, false)).problems!(null, vs)).toEqual([
      {
        path: 'http_routes_config.external_auths[0].upstream_headers[1]',
        message:
          'external auth sso: upstream header authorization is also sent to the check, it would be removed from the request before the check sees it',
      },
      {
        path: 'http_routes_config.external_auths[0].upstream_headers[2]',
        message:
          'external auth sso: upstream header cookie is also sent to the check, it would be removed from the request before the check sees it',
      },
    ]);
  });

  it('flags the upstream headers the controller refuses once lowercased', () => {
    const vs = vsNaming('sso');
    const config = (vs.routeConfig as { value: HttpRoutesConfig }).value;
    config.externalAuths[0].upstreamHeaders = ['Host', 'x-user', 'Content-Length', ':path'];

    const refusal =
      "host, pseudo headers and hop by hop headers can't be set on the upstream request";
    expect(kindRules('vs', context(false, false)).problems!(null, vs)).toEqual([
      { path: 'http_routes_config.external_auths[0].upstream_headers[0]', message: refusal },
      { path: 'http_routes_config.external_auths[0].upstream_headers[2]', message: refusal },
      { path: 'http_routes_config.external_auths[0].upstream_headers[3]', message: refusal },
    ]);
  });

  it('accepts a virtual server whose names are all defined, and one with no http routes', () => {
    const rules = kindRules('vs', context(false, false));

    expect(rules.problems!(null, vsNaming('sso'))).toEqual([]);
    expect(rules.problems!(null, create(VirtualServerSchema, { name: 'tcp' }))).toEqual([]);
  });

  it('asks a value for a new key of a secret only, and masks its values', () => {
    const rules = kindRules('secret', context(false, false));
    const stored = create(SecretSchema, { name: 'dns', data: { KEPT: '' } });
    const edited = create(SecretSchema, {
      name: 'dns',
      data: { KEPT: '', FRESH: '', FILLED: 'v' },
    });

    expect(rules.problems?.(stored, edited)).toEqual([
      { path: 'data', message: 'A new key needs a value: FRESH.' },
    ]);
    expect(rules.problems?.(stored, stored)).toEqual([]);
    expect(rules.problems?.(null, stored)).toHaveLength(1);
    expect(rules.masked).toEqual(['data']);
  });

  it('vip ip is read-only for a non-admin', async () => {
    const vip = create(VIPSchema, { UUID: 'v1', name: 'web-vip', ip: '10.0.0.1' });
    expect(kindRules('vip', context(true, false)).readonly?.(vip)).toEqual([]);

    const fixture = await render('vip', vip, kindRules('vip', context(false, true)));
    button(card('General'), 'Edit').click();
    await settle(fixture);

    const general = card('General');
    expect(general.querySelector<HTMLInputElement>('input[name="ip"]')!.disabled).toBe(true);
    expect(general.querySelector<HTMLInputElement>('input[name="prefix_length"]')!.disabled).toBe(
      true,
    );
    expect(general.querySelector<HTMLInputElement>('input[name="name"]')!.disabled).toBe(false);
  });

  it("creates a non-admin's VIP through registerVip with the ipam on", async () => {
    const fixture = await render('vip', null, kindRules('vip', context(false, true)));

    const explicit = button(panel(), 'Explicit IP');
    expect(explicit.getAttribute('aria-disabled')).toBe('true');
    expect(tooltipOf(fixture, explicit.closest('mat-button-toggle')!)).toBe(
      'Only an admin of the controller can create a VIP.',
    );
    type(card('IP from the ipam').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(panel(), 'Create').click();
    await settle(fixture);

    expect(calls['registerVip'].mock.calls[0][0].name).toBe('api');
    expect(calls['createVIP']).not.toHaveBeenCalled();
    expect(panel().querySelector('[data-card="General"]')).not.toBeNull();
  });

  it("greys a non-admin's VIP Create with the ipam off", async () => {
    features = { ipamEnabled: false };
    const fixture = await render('vip', null, kindRules('vip', context(false, false)));

    expect(panel().textContent).not.toContain('IP from the ipam');
    const createButton = button(panel(), 'Create');
    expect(createButton.getAttribute('aria-disabled')).toBe('true');
    expect(tooltipOf(fixture, createButton)).toBe(
      'Only an admin of the controller can create a VIP.',
    );
  });

  it('keeps the zone and resource type of an instance after create', () => {
    const rules = kindRules('instance', context(false, false));
    expect(rules.readonly?.(storedInstance())).toEqual(['zone_uuid', 'resource_type']);
    expect(rules.readonly?.(null)).toEqual([]);
  });

  it('adds a VIP to an instance through addVipToInstance', async () => {
    const rules = kindRules('instance', context(false, false));
    const edited = create(InstanceSchema, { ...storedInstance(), vipsUuid: ['v1', 'v2'] });

    const saved = await rules.saveCard!['VIPs'](storedInstance(), edited);

    expect(calls['addVipToInstance']).toHaveBeenCalledWith(
      { UUID: 'i1', vipUuid: 'v2' },
      expect.anything(),
    );
    expect(calls['updateInstance']).not.toHaveBeenCalled();
    expect(dialogs.length).toBe(1);
    expect(saved).not.toBeNull();
  });

  it('removes a VIP from an instance through deleteVipFromInstance', async () => {
    const rules = kindRules('instance', context(false, false));
    const edited = create(InstanceSchema, { ...storedInstance(), vipsUuid: [] });

    await rules.saveCard!['VIPs'](storedInstance(), edited);

    expect(calls['deleteVipFromInstance']).toHaveBeenCalledWith(
      { UUID: 'i1', vipUuid: 'v1' },
      expect.anything(),
    );
  });

  it('saves an edited pool through updatePoolOfZone', async () => {
    const rules = kindRules('zone', context(true, true));
    const edited = storedZone();
    edited.pools[0].subnets[0].prefixLength = 16;
    edited.pools.push(create(IpPoolSchema, { name: 'extra' }));

    await rules.saveCard!['Pools'](storedZone(), edited);

    const updated = calls['updatePoolOfZone'].mock.calls[0][0];
    expect(updated.UUID).toBe('z1');
    expect(updated.pool.name).toBe('public');
    expect(updated.pool.subnets[0].prefixLength).toBe(16);
    expect(calls['addPoolToZone'].mock.calls[0][0].pool.name).toBe('extra');
    expect(calls['deletePoolFromZone']).not.toHaveBeenCalled();
    expect(dialogs.length).toBe(2);
  });

  it('removes a pool through deletePoolFromZone', async () => {
    const rules = kindRules('zone', context(true, true));
    const edited = storedZone();
    edited.pools.splice(0, 1);

    await rules.saveCard!['Pools'](storedZone(), edited);

    expect(calls['deletePoolFromZone']).toHaveBeenCalledWith(
      { UUID: 'z1', poolName: 'public' },
      expect.anything(),
    );
  });

  it('refuses a change to a locked pool on its own', () => {
    const rules = kindRules('zone', context(true, true));
    const edited = storedZone();
    edited.pools[1].subnets[0].prefixLength = 16;
    edited.pools[0].subnets[0].prefixLength = 16;

    const problems = rules.problems!(storedZone(), edited);

    expect(problems).toEqual([
      { path: 'pools[1]', message: `The pool managed is locked. ${lockReason}` },
    ]);
  });

  it('refuses removing a locked pool', () => {
    const rules = kindRules('zone', context(true, true));
    const edited = storedZone();
    edited.pools.splice(1, 1);

    const problems = rules.problems!(storedZone(), edited);

    expect(problems).toEqual([
      { path: 'pools', message: `The pool managed is locked. ${lockReason}` },
    ]);
  });

  it('keeps the ip of a vip read-only for an admin while the ipam is on', () => {
    const vip = create(VIPSchema, { UUID: 'v1', name: 'web-vip', ip: '10.0.0.1' });
    expect(kindRules('vip', context(true, true)).readonly?.(vip)).toEqual(['ip']);
    expect(kindRules('vip', context(true, true)).readonly?.(null)).toEqual([]);
  });

  it('refuses registering a VIP into a locked instance', async () => {
    calls['readInstance'].mockResolvedValueOnce({
      instance: create(InstanceSchema, {
        ...storedInstance(),
        metadata: { 'flock.autoprovision': 'true' },
      }),
    });
    const way = kindRules('vip', context(false, true)).createWays![0];
    const request = create(RegisterVipRequestSchema, {
      name: 'api',
      location: { case: 'instanceUuid', value: 'i1' },
    });

    const sent = way.request!.send(request, {});

    await expect(sent).rejects.toBeInstanceOf(FieldsFailure);
    await sent.catch((err: FieldsFailure) =>
      expect(err.errors).toEqual([
        { path: 'instance_uuid', message: `The instance edge is locked. ${lockReason}` },
      ]),
    );
    expect(calls['registerVip']).not.toHaveBeenCalled();
  });

  it('shows the violations of an add on the VIPs field', async () => {
    calls['addVipToInstance'].mockRejectedValueOnce(
      new ConnectError('invalid', Code.InvalidArgument, undefined, [
        {
          desc: ViolationsSchema,
          value: {
            violations: [{ field: { elements: [{ fieldName: 'vip_uuid' }] }, message: 'bad uuid' }],
          },
        },
      ]),
    );
    const rules = kindRules('instance', context(false, false));
    const edited = create(InstanceSchema, { ...storedInstance(), vipsUuid: ['v1', 'v2'] });

    const saved = rules.saveCard!['VIPs'](storedInstance(), edited);

    await saved.catch((err: FieldsFailure) =>
      expect(err.errors).toEqual([{ path: 'vips_uuid', message: 'bad uuid' }]),
    );
    await expect(saved).rejects.toBeInstanceOf(FieldsFailure);
  });

  it('shows the violations of a pool update on that pool', async () => {
    calls['updatePoolOfZone'].mockRejectedValueOnce(
      new ConnectError('invalid', Code.InvalidArgument, undefined, [
        {
          desc: ViolationsSchema,
          value: {
            violations: [
              {
                field: {
                  elements: [
                    { fieldName: 'pool' },
                    { fieldName: 'subnets', subscript: { case: 'index', value: 0n } },
                  ],
                },
                message: 'bad subnet',
              },
            ],
          },
        },
      ]),
    );
    const rules = kindRules('zone', context(true, true));
    const edited = storedZone();
    edited.pools[0].subnets[0].prefixLength = 16;

    const saved = rules.saveCard!['Pools'](storedZone(), edited);

    await saved.catch((err: FieldsFailure) =>
      expect(err.errors).toEqual([{ path: 'pools[0].subnets[0]', message: 'bad subnet' }]),
    );
    await expect(saved).rejects.toBeInstanceOf(FieldsFailure);
  });

  it('greys a locked pool in the pool list', async () => {
    sessionAdmin = true;
    const fixture = await render('zone', storedZone(), kindRules('zone', context(true, true)));

    button(card('Pools'), 'Edit').click();
    await settle(fixture);
    const items = card('Pools').querySelectorAll<HTMLElement>('.item');
    const removeOf = (item: HTMLElement) =>
      item.querySelector<HTMLButtonElement>('button[aria-label="Remove"]')!;
    expect(removeOf(items[0]).getAttribute('aria-disabled')).not.toBe('true');
    const lockedRemove = removeOf(items[1]);
    expect(lockedRemove.getAttribute('aria-disabled')).toBe('true');
    expect(tooltipOf(fixture, lockedRemove)).toBe(`The pool managed is locked. ${lockReason}`);
    lockedRemove.click();
    await settle(fixture);
    expect(card('Pools').querySelectorAll('.item').length).toBe(2);

    items[1].querySelector<HTMLButtonElement>('button.open')!.click();
    await settle(fixture);
    const poolPanel = document.body.querySelectorAll('flock-side-panel')[1];
    expect(poolPanel.textContent).toContain(lockReason);
    expect(poolPanel.textContent).not.toContain('Apply to');
  });

  it('keeps a typed private key out of the confirmation', async () => {
    const fixture = await render(
      'certificate',
      storedCertificate(),
      kindRules('certificate', context(false, false)),
    );

    button(card('Certificate'), 'Edit').click();
    await settle(fixture);
    const key = card('Certificate').querySelector<HTMLInputElement>('[name="private_key"]')!;
    type(key, 'SECRET-KEY');
    await settle(fixture);
    button(card('Certificate'), 'Save').click();
    await settle(fixture);

    expect(JSON.stringify(dialogs)).not.toContain('SECRET-KEY');
    expect(JSON.stringify(dialogs)).toContain('(new value, hidden)');
    expect(calls['updateCertificate'].mock.calls[0][0].certificate.privateKey).toBe('SECRET-KEY');
  });

  it('drops a saved private key from the card, like the controller answer', async () => {
    // UpdateCertificate never returns the key, a typed one included
    calls['updateCertificate'] = vi.fn(async (req: { certificate: Resource }) => ({
      certificate: { ...req.certificate, privateKey: '' },
    }));
    const fixture = await render(
      'certificate',
      storedCertificate(),
      kindRules('certificate', context(false, false)),
    );

    button(card('Certificate'), 'Edit').click();
    await settle(fixture);
    type(
      card('Certificate').querySelector<HTMLInputElement>('[name="private_key"]')!,
      'SECRET-KEY',
    );
    await settle(fixture);
    button(card('Certificate'), 'Save').click();
    await settle(fixture);
    expect(panel().textContent).not.toContain('SECRET-KEY');

    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    dialogs = [];
    button(card('General'), 'Save').click();
    await settle(fixture);

    expect(JSON.stringify(dialogs)).not.toContain('Changed since you opened it');
    expect(calls['updateCertificate']).toHaveBeenCalledTimes(2);
    expect(calls['updateCertificate'].mock.calls[1][0].certificate.privateKey).toBe('');
  });

  it('edits a certificate name without its key', async () => {
    const fixture = await render(
      'certificate',
      storedCertificate(),
      kindRules('certificate', context(false, false)),
    );

    button(card('General'), 'Edit').click();
    await settle(fixture);
    type(card('General').querySelector<HTMLInputElement>('input[name="name"]')!, 'api');
    await settle(fixture);
    button(card('General'), 'Save').click();
    await settle(fixture);

    const sent = calls['updateCertificate'].mock.calls[0][0];
    expect(sent.certificate.name).toBe('api');
    expect(sent.certificate.privateKey).toBe('');
    expect(sent.certificate.certChain).toBe('-----BEGIN CERTIFICATE-----');
  });
});
