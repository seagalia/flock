import { Component, computed, input, output } from '@angular/core';
import { Secret } from '../../gen/flock/api/config/secret/v1/secret_pb';
import { SidePanel } from './side-panel';

// SecretReveal is the one place the dashboard shows the values of a secret. The parent renders it once the owner
// asked for them and removes it on (closed), so the values live only as long as the panel.
@Component({
  selector: 'flock-secret-reveal',
  imports: [SidePanel],
  template: `
    <flock-side-panel
      title="Values of the secret"
      [subject]="secret().name"
      (closed)="closed.emit()"
    >
      <dl>
        @for (entry of entries(); track entry[0]) {
          <dt>{{ entry[0] }}</dt>
          <dd>{{ entry[1] }}</dd>
        }
      </dl>
    </flock-side-panel>
  `,
  styles: `
    dl {
      margin: 16px 0;
    }
    dt {
      font-weight: 500;
      margin-top: 12px;
    }
    dd {
      margin: 4px 0 0;
      padding: 8px 12px;
      font-family: var(--flock-mono);
      font-size: 13px;
      background: var(--flock-code-bg);
      color: var(--flock-code-text);
      border: 1px solid var(--flock-border);
      border-radius: 8px;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
  `,
})
export class SecretReveal {
  readonly secret = input.required<Secret>();
  readonly closed = output<void>();

  protected readonly entries = computed(() => Object.entries(this.secret().data));
}
