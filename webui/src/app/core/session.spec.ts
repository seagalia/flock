import { TestBed } from '@angular/core/testing';
import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import {
  GetTokenResponseSchema,
  MeResponseSchema,
} from '../../gen/flock/services/login/v1/login_pb';
import { provideRouter, Router, UrlTree } from '@angular/router';
import { ConnectError, Code } from '@connectrpc/connect';
import { Api } from './api';
import { Oidc } from './oidc';
import { loggedInGuard, Session } from './session';

describe('Session', () => {
  let session: Session;
  const me = create(MeResponseSchema, { username: 'admin', groups: ['ops'], isAdmin: true });
  const token = create(GetTokenResponseSchema, {
    token: 'access',
    expiresAt: timestampFromDate(new Date(Date.now() + 3600_000)),
  });

  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    TestBed.configureTestingModule({
      providers: [{ provide: Oidc, useValue: { idToken: async () => 'id-token' } }],
    });
    session = TestBed.inject(Session);
  });

  it('authenticates with the flock token after a password sign-in', async () => {
    session.start('flock', me, token);

    expect(session.isLoggedIn()).toBe(true);
    expect(session.kind()).toBe('flock');
    expect(session.user()).toEqual({ username: 'admin', groups: ['ops'], isAdmin: true });
    expect(await session.bearer()).toBe('access');
  });

  it('authenticates with the id token of the provider after an oidc sign-in', async () => {
    session.start('oidc', me);

    expect(session.isLoggedIn()).toBe(true);
    expect(session.kind()).toBe('oidc');
    expect(session.user()).toEqual({ username: 'admin', groups: ['ops'], isAdmin: true });
    expect(await session.bearer()).toBe('id-token');
  });

  it('brings the user back where the session expired', () => {
    session.start('flock', me, token);
    session.expire('/backends?uuid=1');

    expect(session.isLoggedIn()).toBe(false);
    expect(session.user()).toBeNull();
    expect(session.takeReturnUrl()).toBe('/backends?uuid=1');
    expect(session.takeReturnUrl()).toBe('/');
  });

  it('sends no bearer token for a cli session', async () => {
    session.start('cli', me);

    expect(session.kind()).toBe('cli');
    expect(await session.bearer()).toBe('');
  });

  describe('loggedInGuard', () => {
    const runGuard = (meCall: () => Promise<unknown>) => {
      TestBed.resetTestingModule();
      TestBed.configureTestingModule({
        providers: [
          provideRouter([]),
          { provide: Oidc, useValue: { idToken: async () => 'id-token' } },
          { provide: Api, useValue: { login: { me: meCall } } },
        ],
      });
      return TestBed.runInInjectionContext(() => loggedInGuard(null as never, null as never));
    };

    it('starts a cli session when Me answers without a token', async () => {
      const result = await runGuard(async () => me);

      expect(result).toBe(true);
      expect(TestBed.inject(Session).kind()).toBe('cli');
      expect(TestBed.inject(Session).user()?.username).toBe('admin');
    });

    it('goes to the login page when Me is unauthenticated', async () => {
      const result = await runGuard(async () => {
        throw new ConnectError('no', Code.Unauthenticated);
      });

      expect(result).toBeInstanceOf(UrlTree);
      expect(TestBed.inject(Router).serializeUrl(result as UrlTree)).toBe('/login');
      expect(TestBed.inject(Session).isLoggedIn()).toBe(false);
    });

    it('does not ask Me when already signed in', async () => {
      localStorage.setItem('session_kind', 'flock');
      const meCall = vi.fn();

      expect(await runGuard(meCall)).toBe(true);
      expect(meCall).not.toHaveBeenCalled();
    });
  });
});
