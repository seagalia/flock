import { Component, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { Router, RouterLink } from '@angular/router';
import { ConnectError } from '@connectrpc/connect';
import { Api } from '../core/api';
import { Oidc } from '../core/oidc';
import { authHeaders, Session } from '../core/session';
import { Theme } from '../core/theme';

// Callback is where the OIDC provider sends the user back: it trades the code for the tokens, asks the controller
// who the ID token is and starts the session
@Component({
  selector: 'flock-callback',
  imports: [MatButtonModule, MatIconModule, MatProgressBarModule, RouterLink],
  template: `
    <main class="login">
      <section class="card">
        @if (!error()) {
          <mat-progress-bar mode="indeterminate" class="progress" />
        }
        <img
          class="lockup"
          [src]="theme.isDark() ? 'brand/lockup-dark.svg' : 'brand/lockup.svg'"
          alt="Flock"
          width="280"
          height="120"
        />
        @if (error()) {
          <p class="error" role="alert">
            <mat-icon>error</mat-icon>
            {{ error() }}
          </p>
          <a mat-flat-button routerLink="/login">Back to the login page</a>
        } @else {
          <p class="tagline">Signing in...</p>
        }
      </section>
    </main>
  `,
  styleUrl: './login.scss',
})
export class Callback {
  private api = inject(Api);
  private oidc = inject(Oidc);
  private session = inject(Session);
  private router = inject(Router);
  protected theme = inject(Theme);

  protected error = signal('');

  constructor() {
    this.signIn();
  }

  private async signIn() {
    try {
      const returnUrl = await this.oidc.completeSignIn();
      const idToken = (await this.oidc.idToken()) ?? '';
      const me = await this.api.login.me({}, { headers: authHeaders(idToken) });
      this.session.start('oidc', me);
      await this.router.navigateByUrl(returnUrl);
    } catch (err) {
      this.error.set(`Sign-in failed: ${ConnectError.from(err).rawMessage}`);
    }
  }
}
