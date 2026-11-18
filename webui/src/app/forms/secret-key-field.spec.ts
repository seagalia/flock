import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { provideRouter } from '@angular/router';
import { create, DescMessage, Message } from '@bufbuild/protobuf';
import { of } from 'rxjs';
import { HeaderValue, HeaderValueSchema } from '../../gen/flock/api/config/core/v1/base_pb';
import { HealthCheckSchema } from '../../gen/flock/api/config/core/v1/healthcheck_pb';
import { URL_UserInfo, URL_UserInfoSchema } from '../../gen/flock/api/config/core/v1/url_pb';
import { SecretSchema } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { SecretKeyRefSchema } from '../../gen/flock/type/v1/secret_pb';
import { Api } from '../core/api';
import { ResourceCreations } from '../core/resource-creations';
import { Session } from '../core/session';
import { Access } from '../edit/access';
import { ProtoForm } from './proto-form';
import { secretOneof } from './secret-key-field';

const dbUuid = '11111111-2222-3333-4444-555555555555';
const secrets = [create(SecretSchema, { UUID: dbUuid, name: 'db', data: { token: '' } })];

@Component({
  imports: [ProtoForm],
  template: `<flock-proto-form [schema]="schema()" [(value)]="value" [allReadonly]="readOnly()" />`,
})
class FormHost {
  schema = signal<DescMessage>(HeaderValueSchema);
  value = signal<Message>(create(HeaderValueSchema));
  readOnly = signal(false);
}

async function settle(fixture: { whenStable(): Promise<unknown> }) {
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
}

async function render(schema: DescMessage, value: Message, readOnly = false) {
  const fixture = TestBed.createComponent(FormHost);
  fixture.componentInstance.schema.set(schema);
  fixture.componentInstance.value.set(value);
  fixture.componentInstance.readOnly.set(readOnly);
  await settle(fixture);
  return fixture;
}

function toggle(root: HTMLElement, text: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('mat-button-toggle button')].find(
    (b) => b.textContent?.trim() === text,
  );
  if (!found) {
    throw new Error(`no toggle ${text}`);
  }
  return found;
}

function openSelect(root: HTMLElement, label: string) {
  const field = [
    ...root.querySelectorAll<HTMLElement>('flock-secret-ref-picker mat-form-field'),
  ].find((f) => f.textContent?.includes(label));
  field!.querySelector<HTMLElement>('.mat-mdc-select-trigger')!.click();
}

function pickOption(text: string) {
  const panels = document.querySelectorAll('.mat-mdc-select-panel');
  const options = [
    ...(panels[panels.length - 1]?.querySelectorAll<HTMLElement>('mat-option') ?? []),
  ];
  options.find((o) => o.textContent?.includes(text))!.click();
}

describe('secretOneof', () => {
  it('finds the plain case and the secret case of a header value', () => {
    const found = secretOneof(HeaderValueSchema.oneofs[0]);

    expect(found?.text?.name).toBe('value');
    expect(found?.ref.name).toBe('value_from_secret');
  });

  it('leaves out the plain case of a password, which only agents set', () => {
    const found = secretOneof(URL_UserInfoSchema.oneofs[0]);

    expect(found?.text).toBeNull();
    expect(found?.ref.name).toBe('password_from_secret');
  });

  it('gives nothing for a oneof of other messages', () => {
    expect(secretOneof(HealthCheckSchema.oneofs[0])).toBeNull();
  });
});

describe('SecretKeyField', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: Api, useValue: { list: vi.fn(async () => secrets) } },
        { provide: ResourceCreations, useValue: { start: () => undefined } },
        {
          provide: Access,
          useValue: { check: async () => null, features: async () => ({ ipamEnabled: false }) },
        },
        {
          provide: Session,
          useValue: { user: () => ({ username: 'alice', groups: [], isAdmin: false }) },
        },
        { provide: MatDialog, useValue: { open: () => ({ afterClosed: () => of(true) }) } },
      ],
    });
  });

  afterEach(() => {
    document
      .querySelectorAll('flock-side-panel, .cdk-overlay-container')
      .forEach((e) => e.remove());
  });

  it('writes a header value typed as text', async () => {
    const fixture = await render(HeaderValueSchema, create(HeaderValueSchema, { key: 'x-token' }));
    const root: HTMLElement = fixture.nativeElement;

    const input = root.querySelector<HTMLInputElement>(
      'flock-secret-key-field input[name="value"]',
    )!;
    input.value = 'abc';
    input.dispatchEvent(new Event('input'));
    await settle(fixture);

    const value = fixture.componentInstance.value() as HeaderValue;
    expect(value.valueSource).toEqual({ case: 'value', value: 'abc' });
  });

  it('points a header value at a secret once Secret is picked', async () => {
    const fixture = await render(HeaderValueSchema, create(HeaderValueSchema, { key: 'x-token' }));
    const root: HTMLElement = fixture.nativeElement;

    toggle(root, 'Secret').click();
    await settle(fixture);
    openSelect(root, 'Secret');
    await settle(fixture);
    pickOption('db');
    await settle(fixture);
    openSelect(root, 'Key');
    await settle(fixture);
    pickOption('token');
    await settle(fixture);

    const value = fixture.componentInstance.value() as HeaderValue;
    expect(value.valueSource.case).toBe('valueFromSecret');
    expect(value.valueSource.value).toEqual(
      create(SecretKeyRefSchema, { secretUuid: dbUuid, key: 'token' }),
    );
  });

  it('only offers a secret for a password', async () => {
    const fixture = await render(URL_UserInfoSchema, create(URL_UserInfoSchema, { user: 'lb' }));
    const root: HTMLElement = fixture.nativeElement;

    expect(root.querySelector('flock-secret-key-field mat-button-toggle')).toBeNull();
    expect(root.querySelector('input[name="password"]')).toBeNull();
    expect(root.querySelector('flock-secret-key-field flock-secret-ref-picker')).not.toBeNull();
  });

  it('shows a read-only reference as a link to its secret', async () => {
    const userInfo: URL_UserInfo = create(URL_UserInfoSchema, {
      user: 'lb',
      passwordSource: {
        case: 'passwordFromSecret',
        value: create(SecretKeyRefSchema, { secretUuid: dbUuid, key: 'token' }),
      },
    });
    const fixture = await render(URL_UserInfoSchema, userInfo, true);
    const root: HTMLElement = fixture.nativeElement;

    expect(
      root.querySelector('flock-secret-key-field flock-secret-ref-link')?.textContent,
    ).toContain('token');
    expect(root.querySelector('flock-secret-key-field flock-secret-ref-picker')).toBeNull();
  });
});
