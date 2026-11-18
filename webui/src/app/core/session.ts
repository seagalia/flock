import { inject, Injectable, signal } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { GetTokenResponse, MeResponse } from '../../gen/flock/services/login/v1/login_pb';
import { Api } from './api';
import { Oidc } from './oidc';

// SessionKind is how the user signed in. A cli session comes from `flock dashboard`, whose local server proxies the api
// with the token of the cli, the other kinds hold a flock token or the ID token of the OIDC provider
export type SessionKind = 'flock' | 'oidc' | 'cli';

export interface SessionUser {
  username: string;
  groups: string[];
  isAdmin: boolean;
}

const keys = {
  kind: 'session_kind',
  username: 'username',
  groups: 'groups',
  isAdmin: 'is_admin',
  token: 'access_token',
};
const returnUrlKey = 'return_url';

// authHeaders are the headers the controller reads to authenticate a user
export function authHeaders(token: string): Record<string, string> {
  return { authorization: `Bearer ${token}` };
}

@Injectable({ providedIn: 'root' })
export class Session {
  private oidc = inject(Oidc);
  readonly user = signal<SessionUser | null>(this.loadUser());

  start(kind: SessionKind, me: MeResponse, token?: GetTokenResponse) {
    localStorage.setItem(keys.kind, kind);
    if (token) {
      localStorage.setItem(keys.token, token.token);
    }
    localStorage.setItem(keys.username, me.username);
    localStorage.setItem(keys.groups, JSON.stringify(me.groups));
    localStorage.setItem(keys.isAdmin, JSON.stringify(me.isAdmin));
    this.user.set(this.loadUser());
  }

  clear() {
    Object.values(keys).forEach((key) => localStorage.removeItem(key));
    this.user.set(null);
  }

  // expire ends a session the controller refused, the user comes back to returnUrl after logging in again
  expire(returnUrl: string) {
    this.clear();
    if (!returnUrl.startsWith('/login')) {
      sessionStorage.setItem(returnUrlKey, returnUrl);
    }
  }

  takeReturnUrl(): string {
    const returnUrl = sessionStorage.getItem(returnUrlKey) ?? '/';
    sessionStorage.removeItem(returnUrlKey);
    return returnUrl;
  }

  isLoggedIn(): boolean {
    return this.kind() !== null;
  }

  kind(): SessionKind | null {
    return localStorage.getItem(keys.kind) as SessionKind | null;
  }

  // bearer is the token of the authorization header, oidc-client-ts keeps the ID token renewed in its own store
  async bearer(): Promise<string> {
    // the cli server adds the token itself
    if (this.kind() === 'cli') {
      return '';
    }
    if (this.kind() === 'oidc') {
      return (await this.oidc.idToken()) ?? '';
    }
    return localStorage.getItem(keys.token) ?? '';
  }

  private loadUser(): SessionUser | null {
    const username = localStorage.getItem(keys.username);
    if (!username || !this.isLoggedIn()) {
      return null;
    }
    return {
      username,
      groups: JSON.parse(localStorage.getItem(keys.groups) ?? '[]'),
      isAdmin: JSON.parse(localStorage.getItem(keys.isAdmin) ?? 'false'),
    };
  }
}

export const loggedInGuard: CanActivateFn = async () => {
  const session = inject(Session);
  if (session.isLoggedIn()) {
    return true;
  }
  const api = inject(Api);
  const router = inject(Router);
  // under `flock dashboard` Me answers without any header, a controller refuses it, and the login client
  // shows no error. Any failure of Me (network, 5xx) lands on the login page, which then shows the real state.
  try {
    const me = await api.login.me({});
    session.start('cli', me);
    return true;
  } catch {
    return router.createUrlTree(['/login']);
  }
};
