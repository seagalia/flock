import { Injectable } from '@angular/core';
import type { UserManager } from 'oidc-client-ts';
import { OidcProvider } from '../../gen/flock/services/login/v1/login_pb';

interface StoredProvider {
  issuerUrl: string;
  clientId: string;
  scopes: string[];
}

const providerKey = 'oidc_provider';

// Oidc signs in at the OIDC provider of the controller with the authorization code flow and PKCE. The provider is
// kept in localStorage: the callback page and every reload build the same UserManager again, which finds the
// user it stored and keeps renewing its tokens. oidc-client-ts is imported lazily so that dashboards of password
// logins never download it.
@Injectable({ providedIn: 'root' })
export class Oidc {
  private manager: Promise<UserManager> | null = null;

  constructor() {
    const stored = localStorage.getItem(providerKey);
    if (stored) {
      this.manager = this.build(JSON.parse(stored));
    }
  }

  configure(provider: OidcProvider) {
    const stored: StoredProvider = {
      issuerUrl: provider.issuerUrl,
      clientId: provider.clientId,
      scopes: provider.scopes,
    };
    localStorage.setItem(providerKey, JSON.stringify(stored));
    this.manager?.then((previous) => previous.stopSilentRenew());
    this.manager = this.build(stored);
  }

  // signIn leaves the dashboard for the provider, which sends the user back to the callback page
  async signIn(returnUrl: string): Promise<void> {
    await (await this.configured()).signinRedirect({ state: returnUrl });
  }

  // completeSignIn trades the code of the callback url for the tokens and gives back the url the user started from
  async completeSignIn(): Promise<string> {
    const user = await (await this.configured()).signinRedirectCallback();
    return typeof user.state === 'string' ? user.state : '/';
  }

  async idToken(): Promise<string | null> {
    if (!this.manager) {
      return null;
    }
    const user = await (await this.manager).getUser();
    return user?.id_token ?? null;
  }

  async signOut(): Promise<void> {
    const manager = await this.configured();
    const endSessionEndpoint = await manager.metadataService.getEndSessionEndpoint();
    if (!endSessionEndpoint) {
      await manager.removeUser();
      return;
    }
    await manager.signoutRedirect();
  }

  private async build(provider: StoredProvider): Promise<UserManager> {
    const { UserManager, WebStorageStateStore } = await import('oidc-client-ts');
    const origin = window.location.origin;
    return new UserManager({
      authority: provider.issuerUrl,
      client_id: provider.clientId,
      redirect_uri: `${origin}/dashboard/callback`,
      post_logout_redirect_uri: `${origin}/dashboard/login`,
      response_type: 'code',
      scope: provider.scopes.join(' '),
      userStore: new WebStorageStateStore({ store: localStorage }),
      automaticSilentRenew: true,
    });
  }

  private async configured(): Promise<UserManager> {
    if (!this.manager) {
      throw new Error(
        'the OIDC provider of the controller is unknown, sign in again from the login page',
      );
    }
    return this.manager;
  }
}
