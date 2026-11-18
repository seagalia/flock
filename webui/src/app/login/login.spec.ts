import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, provideRouter, Router } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { ServiceAuthSchema } from '../../gen/flock/services/login/v1/login_pb';
import { Api } from '../core/api';
import { Oidc } from '../core/oidc';
import { Theme } from '../core/theme';
import { Tour } from '../core/tour';
import { Login } from './login';

describe('Login', () => {
  const oidcProvider = {
    issuerUrl: 'https://idp.example.com/realms/flock',
    clientId: 'flock-dashboard',
    scopes: ['openid'],
  };
  let navigate: ReturnType<typeof vi.spyOn>;
  let oidc: { configure: ReturnType<typeof vi.fn>; signIn: ReturnType<typeof vi.fn> };
  let tour: { askOffer: ReturnType<typeof vi.fn> };

  const render = async (type: string, fragment: string | null = null) => {
    const serviceAuth = create(ServiceAuthSchema, {
      type,
      oidc: type === 'OIDC' ? oidcProvider : undefined,
    });
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: ActivatedRoute, useValue: { snapshot: { fragment } } },
        { provide: Api, useValue: { login: { getServiceAuth: async () => serviceAuth } } },
        { provide: Oidc, useValue: oidc },
        { provide: Tour, useValue: tour },
        // jsdom has no matchMedia for the theme to follow
        { provide: Theme, useValue: { isDark: () => false } },
      ],
    });
    navigate = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    const fixture = TestBed.createComponent(Login);
    await fixture.whenStable();
    fixture.detectChanges();
    return fixture.nativeElement as HTMLElement;
  };

  beforeEach(() => {
    sessionStorage.clear();
    oidc = { configure: vi.fn(), signIn: vi.fn().mockResolvedValue(undefined) };
    tour = { askOffer: vi.fn() };
  });

  for (const type of ['BASIC', 'LDAP']) {
    it(`asks for a username and a password for ${type}`, async () => {
      const page = await render(type);

      expect(page.querySelector('input[name="username"]')).not.toBeNull();
      expect(page.querySelector('input[name="password"]')).not.toBeNull();
      expect(oidc.configure).not.toHaveBeenCalled();
    });
  }

  it('fills the credentials flock dev gives in the fragment, then drops it from the address', async () => {
    const page = await render('BASIC', 'username=admin&password=admin');

    expect(page.querySelector<HTMLInputElement>('input[name="username"]')!.value).toBe('admin');
    expect(page.querySelector<HTMLInputElement>('input[name="password"]')!.value).toBe('admin');
    expect(navigate).toHaveBeenCalledWith([], { replaceUrl: true });
  });

  it('asks for the offer of the tour when flock dev gives it in the fragment', async () => {
    await render('BASIC', 'username=admin&password=admin');
    expect(tour.askOffer).not.toHaveBeenCalled();

    TestBed.resetTestingModule();
    await render('BASIC', 'username=admin&password=admin&tour=offer');
    expect(tour.askOffer).toHaveBeenCalled();
  });

  it('signs in at the oidc provider with a single button', async () => {
    sessionStorage.setItem('return_url', '/backends');
    const page = await render('OIDC');

    expect(page.querySelector('form')).toBeNull();
    const buttons = page.querySelectorAll('button');
    expect(buttons.length).toBe(1);
    expect(buttons[0].textContent).toContain('Sign in with idp.example.com');
    expect(oidc.configure).toHaveBeenCalledWith(expect.objectContaining(oidcProvider));

    buttons[0].click();

    expect(oidc.signIn).toHaveBeenCalledWith('/backends');
  });
});
