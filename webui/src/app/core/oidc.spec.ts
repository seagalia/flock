import { TestBed } from '@angular/core/testing';
import { create } from '@bufbuild/protobuf';
import { User, UserManager, WebStorageStateStore } from 'oidc-client-ts';
import { OidcProviderSchema } from '../../gen/flock/services/login/v1/login_pb';
import { Oidc } from './oidc';

describe('Oidc', () => {
  const provider = create(OidcProviderSchema, {
    issuerUrl: 'https://idp.example.com/realms/flock',
    clientId: 'flock-dashboard',
    scopes: ['openid', 'profile', 'groups'],
  });

  const user = (idToken: string, returnUrl?: string) =>
    new User({
      id_token: idToken,
      access_token: 'access',
      token_type: 'Bearer',
      profile: { sub: 'u', iss: provider.issuerUrl, aud: provider.clientId, exp: 0, iat: 0 },
      userState: returnUrl,
    });

  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('redirects to the provider with the code flow and keeps the return url in the state', async () => {
    const signinRedirect = vi.spyOn(UserManager.prototype, 'signinRedirect').mockResolvedValue();
    const oidc = TestBed.inject(Oidc);

    oidc.configure(provider);
    await oidc.signIn('/backends?uuid=1');

    expect(signinRedirect).toHaveBeenCalledWith({ state: '/backends?uuid=1' });
    const manager = signinRedirect.mock.contexts[0] as UserManager;
    expect(manager.settings).toMatchObject({
      authority: 'https://idp.example.com/realms/flock',
      client_id: 'flock-dashboard',
      redirect_uri: `${window.location.origin}/dashboard/callback`,
      post_logout_redirect_uri: `${window.location.origin}/dashboard/login`,
      response_type: 'code',
      scope: 'openid profile groups',
      automaticSilentRenew: true,
    });
    expect(manager.settings.userStore).toBeInstanceOf(WebStorageStateStore);
  });

  it('gives back the return url once the provider answered', async () => {
    vi.spyOn(UserManager.prototype, 'signinRedirectCallback').mockResolvedValue(
      user('id-token', '/backends?uuid=1'),
    );
    const oidc = TestBed.inject(Oidc);
    oidc.configure(provider);

    expect(await oidc.completeSignIn()).toBe('/backends?uuid=1');
  });

  it('reads the id token of the stored user', async () => {
    vi.spyOn(UserManager.prototype, 'getUser').mockResolvedValue(user('id-token'));
    const oidc = TestBed.inject(Oidc);
    oidc.configure(provider);

    expect(await oidc.idToken()).toBe('id-token');
  });

  it('has no id token before it is configured', async () => {
    expect(await TestBed.inject(Oidc).idToken()).toBeNull();
  });

  it('keeps the provider across a reload of the page', async () => {
    const signinRedirect = vi.spyOn(UserManager.prototype, 'signinRedirect').mockResolvedValue();
    TestBed.inject(Oidc).configure(provider);
    TestBed.resetTestingModule();

    await TestBed.inject(Oidc).signIn('/');

    const manager = signinRedirect.mock.contexts[0] as UserManager;
    expect(manager.settings.client_id).toBe('flock-dashboard');
  });

  it('signs out at the provider when it has an end session endpoint', async () => {
    const signoutRedirect = vi.spyOn(UserManager.prototype, 'signoutRedirect').mockResolvedValue();
    const removeUser = vi.spyOn(UserManager.prototype, 'removeUser').mockResolvedValue();
    const oidc = TestBed.inject(Oidc);
    oidc.configure(provider);
    vi.spyOn(UserManager.prototype, 'metadataService', 'get').mockReturnValue({
      getEndSessionEndpoint: async () => 'https://idp.example.com/logout',
    } as unknown as UserManager['metadataService']);

    await oidc.signOut();

    expect(signoutRedirect).toHaveBeenCalled();
    expect(removeUser).not.toHaveBeenCalled();
  });

  it('only forgets the user when the provider has no end session endpoint', async () => {
    const signoutRedirect = vi.spyOn(UserManager.prototype, 'signoutRedirect').mockResolvedValue();
    const removeUser = vi.spyOn(UserManager.prototype, 'removeUser').mockResolvedValue();
    vi.spyOn(UserManager.prototype, 'metadataService', 'get').mockReturnValue({
      getEndSessionEndpoint: async () => undefined,
    } as unknown as UserManager['metadataService']);
    const oidc = TestBed.inject(Oidc);
    oidc.configure(provider);

    await oidc.signOut();

    expect(signoutRedirect).not.toHaveBeenCalled();
    expect(removeUser).toHaveBeenCalled();
  });
});
