import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { MeResponseSchema } from '../../gen/flock/services/login/v1/login_pb';
import { Api } from './api';
import { Oidc } from './oidc';
import { Session } from './session';

describe('Api authentication', () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  const me = create(MeResponseSchema, { username: 'admin', groups: [], isAdmin: true });

  // the response is not a valid answer and the call fails, only the request it made matters
  const sentAuthorization = async (): Promise<string | null> => {
    fetchMock.mockResolvedValue(new Response('', { status: 500 }));
    await TestBed.inject(Api)
      .zone.listZone({})
      .catch(() => undefined);
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    return new Headers(init.headers).get('authorization');
  };

  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: Oidc, useValue: { idToken: async () => '' } }],
    });
  });

  afterEach(() => vi.unstubAllGlobals());

  it('sends no authorization header for a cli session', async () => {
    TestBed.inject(Session).start('cli', me);

    expect(await sentAuthorization()).toBeNull();
  });

  it('sends the bearer token for a flock session', async () => {
    localStorage.setItem('access_token', 'abc');
    TestBed.inject(Session).start('flock', me);

    expect(await sentAuthorization()).toBe('Bearer abc');
  });
});
