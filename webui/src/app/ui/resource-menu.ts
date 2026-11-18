import { Clipboard } from '@angular/cdk/clipboard';
import { Component, computed, inject, input, signal } from '@angular/core';
import { Message } from '@bufbuild/protobuf';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Notifier } from '../core/notifier';
import { cliCommand, Kind, kindOf, Resource } from '../core/resources';
import { copyToClipboard } from './copy-button';
import { RawDetails } from './raw-details';

export interface MenuItem {
  label: string;
  icon: string;
  // the reason the item is greyed, shown as a tooltip
  disabled?: string;
  // colors the item, primary for the main action of a row and danger for one that destroys it
  tone?: 'primary' | 'danger';
  run: () => void;
}

// ResourceMenu is the ⋮ menu of a row: the raw resource in a side panel and what to copy from it
@Component({
  selector: 'flock-resource-menu',
  imports: [MatMenuModule, MatTooltipModule, MatIconModule, MatButtonModule, RawDetails],
  template: `
    <button
      mat-icon-button
      aria-label="Actions"
      [matMenuTriggerFor]="menu"
      (click)="$event.stopPropagation()"
    >
      <mat-icon>more_vert</mat-icon>
    </button>
    <mat-menu #menu="matMenu">
      @for (item of extraItems(); track item.label) {
        <button
          mat-menu-item
          [class.primary]="item.tone === 'primary'"
          [class.danger]="item.tone === 'danger'"
          [disabled]="!!item.disabled"
          disabledInteractive
          [matTooltip]="item.disabled ?? ''"
          (click)="runItem(item)"
        >
          <mat-icon>{{ item.icon }}</mat-icon>
          <span>{{ item.label }}</span>
        </button>
      }
      <button mat-menu-item (click)="showRaw.set(true)">
        <mat-icon>code</mat-icon>
        <span>Raw Details</span>
      </button>
      @if (uuid(); as uuid) {
        <button mat-menu-item (click)="copy(uuid)">
          <mat-icon>content_copy</mat-icon>
          <span>Copy UUID</span>
        </button>
      }
      @if (command(); as command) {
        <button mat-menu-item (click)="copy(command)">
          <mat-icon>terminal</mat-icon>
          <span>Copy CLI command</span>
        </button>
      }
    </mat-menu>

    @if (showRaw()) {
      <flock-raw-details [resource]="resource()" (closed)="showRaw.set(false)" />
    }
  `,
  styles: `
    .primary:not([aria-disabled='true']),
    .primary:not([aria-disabled='true']) mat-icon {
      color: var(--mat-sys-primary);
    }
    .danger:not([aria-disabled='true']),
    .danger:not([aria-disabled='true']) mat-icon {
      color: var(--flock-error-text);
    }
  `,
})
export class ResourceMenu {
  readonly resource = input.required<Message>();
  readonly extraItems = input<MenuItem[]>([]);
  protected showRaw = signal(false);
  private clipboard = inject(Clipboard);
  private notifier = inject(Notifier);

  // null for the messages which are not resources, like the permissions
  private kind = computed<Kind | null>(() => {
    try {
      return kindOf(this.resource());
    } catch {
      return null;
    }
  });
  protected uuid = computed(() => (this.kind() ? (this.resource() as Resource).UUID : undefined));
  protected command = computed(() => {
    const kind = this.kind();
    return kind ? cliCommand(kind, this.resource() as Resource) : null;
  });

  // disabledInteractive keeps the item clickable for its tooltip, so the click itself must be ignored
  protected runItem(item: MenuItem) {
    if (item.disabled) {
      return;
    }
    item.run();
  }

  protected copy(text: string) {
    copyToClipboard(this.clipboard, this.notifier, text);
  }
}
