import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { MeResponseSchema } from '../../gen/flock/services/login/v1/login_pb';
import { Api } from '../core/api';
import { Oidc } from '../core/oidc';
import { Session } from '../core/session';
import { Theme } from '../core/theme';
import { Callback } from './callback';

describe('Callback', () => {
  const me = vi.fn();
  let completeSignIn: () => Promise<string>;

  const render = async () => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: Api, useValue: { login: { me } } },
        // jsdom has no matchMedia for the theme to follow
        { provide: Theme, useValue: { isDark: () => false } },
        {
          provide: Oidc,
          useValue: { completeSignIn: () => completeSignIn(), idToken: async () => 'id-token' },
        },
      ],
    });
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
    const fixture = TestBed.createComponent(Callback);
    await fixture.whenStable();
    fixture.detectChanges();
    return { navigate, page: fixture.nativeElement as HTMLElement };
  };

  beforeEach(() => {
    localStorage.clear();
    me.mockReset();
    completeSignIn = async () => '/backends?uuid=1';
  });

  it('starts the session with the id token and goes back to the return url', async () => {
    me.mockResolvedValue(create(MeResponseSchema, { username: 'alice', groups: ['ops'] }));

    const { navigate } = await render();

    expect(me).toHaveBeenCalledWith({}, { headers: { authorization: 'Bearer id-token' } });
    const session = TestBed.inject(Session);
    expect(session.kind()).toBe('oidc');
    expect(session.user()?.username).toBe('alice');
    expect(navigate).toHaveBeenCalledWith('/backends?uuid=1');
  });

  it('shows why the sign-in failed', async () => {
    completeSignIn = async () => {
      throw new Error('No matching state found in storage');
    };

    const { navigate, page } = await render();

    expect(page.textContent).toContain('No matching state found in storage');
    expect(navigate).not.toHaveBeenCalled();
    expect(TestBed.inject(Session).isLoggedIn()).toBe(false);
  });
});
