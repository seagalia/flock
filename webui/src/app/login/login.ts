import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { ActivatedRoute, Router } from '@angular/router';
import { ConnectError } from '@connectrpc/connect';
import { Api } from '../core/api';
import { Oidc } from '../core/oidc';
import { authHeaders, Session } from '../core/session';
import { Theme } from '../core/theme';
import { Tour } from '../core/tour';

// Login signs in with the username and password of a BASIC or LDAP login service, or at the provider of an OIDC one
@Component({
  selector: 'flock-login',
  imports: [
    FormsModule,
    MatFormFieldModule,
    MatInputModule,
    MatButtonModule,
    MatIconModule,
    MatProgressBarModule,
  ],
  templateUrl: './login.html',
  styleUrl: './login.scss',
})
export class Login {
  private api = inject(Api);
  private session = inject(Session);
  private router = inject(Router);
  private oidc = inject(Oidc);
  private tour = inject(Tour);
  protected theme = inject(Theme);

  protected isPasswordLogin = signal(false);
  protected oidcIssuerHost = signal('');
  protected username = '';
  protected password = '';
  protected busy = signal(false);
  protected error = signal('');

  constructor() {
    this.fillFromFragment();
    this.api.login
      .getServiceAuth({})
      .then((serviceAuth) => {
        if (serviceAuth.type === 'OIDC' && serviceAuth.oidc) {
          this.oidc.configure(serviceAuth.oidc);
          this.oidcIssuerHost.set(new URL(serviceAuth.oidc.issuerUrl).host);
          return;
        }
        this.isPasswordLogin.set(true);
      })
      .catch((err) =>
        this.error.set(`Login service unavailable: ${ConnectError.from(err).rawMessage}`),
      );
  }

  // flock dev opens the page with the credentials of its admin and the offer of the tour in the fragment, which the
  // browser never sends to the server. They leave the address bar once read, so they stay out of the history.
  private fillFromFragment() {
    const fragment = inject(ActivatedRoute).snapshot.fragment;
    if (!fragment) {
      return;
    }
    const params = new URLSearchParams(fragment);
    this.username = params.get('username') ?? '';
    this.password = params.get('password') ?? '';
    if (params.get('tour') === 'offer') {
      this.tour.askOffer();
    }
    void this.router.navigate([], { replaceUrl: true });
  }

  protected async loginWithPassword() {
    this.busy.set(true);
    this.error.set('');
    try {
      const token = await this.api.login.getToken({
        username: this.username,
        password: this.password,
      });
      const me = await this.api.login.me({}, { headers: authHeaders(token.token) });
      this.session.start('flock', me, token);
      await this.router.navigateByUrl(this.session.takeReturnUrl());
    } catch (err) {
      this.error.set(ConnectError.from(err).rawMessage);
    } finally {
      this.busy.set(false);
    }
  }

  protected async loginWithOidc() {
    this.busy.set(true);
    this.error.set('');
    try {
      // busy stays on while the browser leaves for the provider
      await this.oidc.signIn(this.session.takeReturnUrl());
    } catch (err) {
      this.error.set(`OIDC provider unavailable: ${(err as Error).message}`);
      this.busy.set(false);
    }
  }
}
