import { Clipboard } from '@angular/cdk/clipboard';
import { Component, inject, input } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Notifier } from '../core/notifier';

// CopyButton copies text to the clipboard, inside a clickable row it does not toggle the row
@Component({
  selector: 'flock-copy-button',
  imports: [MatButtonModule, MatIconModule, MatTooltipModule],
  template: `
    <button
      mat-icon-button
      class="copy"
      [attr.aria-label]="'Copy ' + text()"
      [matTooltip]="'Copy ' + text()"
      (click)="copy(); $event.stopPropagation()"
    >
      <mat-icon>content_copy</mat-icon>
    </button>
  `,
  styles: `
    .copy {
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
export class CopyButton {
  readonly text = input.required<string>();

  private clipboard = inject(Clipboard);
  private notifier = inject(Notifier);

  copy() {
    copyToClipboard(this.clipboard, this.notifier, this.text());
  }
}

export function copyToClipboard(clipboard: Clipboard, notifier: Notifier, text: string) {
  if (clipboard.copy(text)) {
    notifier.info(`Copied ${text}`);
  }
}
