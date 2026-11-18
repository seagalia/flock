import { Clipboard } from '@angular/cdk/clipboard';
import { Component, computed, inject, input } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatTooltipModule } from '@angular/material/tooltip';
import { VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Notifier } from '../core/notifier';
import { testCommands, TestRoute } from '../core/test-commands';
import { copyToClipboard } from './copy-button';

// TestCommandsMenu copies a command sending traffic to a virtual server, or to one of its routes, through a vip
// (curl, openssl, nc)
@Component({
  selector: 'flock-test-commands-menu',
  imports: [MatButtonModule, MatIconModule, MatMenuModule, MatTooltipModule],
  template: `
    @if (commands().length > 0) {
      <button
        mat-icon-button
        class="open"
        aria-label="Copy a test command"
        matTooltip="Copy a test command"
        [matMenuTriggerFor]="menu"
        (click)="$event.stopPropagation()"
      >
        <mat-icon>terminal</mat-icon>
      </button>
      <mat-menu #menu="matMenu">
        @for (one of commands(); track one.command) {
          <button
            mat-menu-item
            [matTooltip]="one.command"
            matTooltipPosition="right"
            (click)="copy(one.command)"
          >
            <mat-icon>content_copy</mat-icon>
            <span>{{ one.label }}</span>
          </button>
        }
      </mat-menu>
    }
  `,
  styles: `
    .open {
      // material centers the icon from these two sizes
      --mat-icon-button-state-layer-size: 28px;
      --mat-icon-button-icon-size: 16px;
      mat-icon {
        font-size: 16px;
        width: 16px;
        height: 16px;
      }
    }
  `,
})
export class TestCommandsMenu {
  readonly vip = input.required<VIP>();
  readonly vs = input.required<VirtualServer>();
  readonly route = input<TestRoute>();

  protected commands = computed(() => testCommands(this.vip(), this.vs(), this.route()));

  private clipboard = inject(Clipboard);
  private notifier = inject(Notifier);

  protected copy(command: string) {
    copyToClipboard(this.clipboard, this.notifier, command);
  }
}
