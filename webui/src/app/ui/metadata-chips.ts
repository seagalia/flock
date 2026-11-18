import { Clipboard } from '@angular/cdk/clipboard';
import { Component, computed, inject, input } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterLink } from '@angular/router';
import { Notifier } from '../core/notifier';
import { kinds } from '../core/resources';
import { parseSecretRef } from '../core/secret-refs';
import { copyToClipboard } from './copy-button';

export interface MetadataChip {
  label: string;
  isFlock: boolean;
  // set when the value points at a secret: the chip opens it instead of copying the reference
  secretUuid?: string;
}

// metadataChips lists the user's own keys first, then the flock.* keys flock acts on or writes, each group sorted
export function metadataChips(metadata: Record<string, string> | undefined): MetadataChip[] {
  const values = metadata ?? {};
  const chips = Object.keys(values)
    .sort()
    .map((key): MetadataChip => {
      const value = values[key];
      return {
        label: `${key}=${value}`,
        isFlock: key.startsWith('flock.'),
        secretUuid: parseSecretRef(value)?.secretUuid,
      };
    });
  const own = chips.filter((chip) => !chip.isFlock);
  const flock = chips.filter((chip) => chip.isFlock);
  return [...own, ...flock];
}

@Component({
  selector: 'flock-metadata-chips',
  imports: [MatIconModule, MatTooltipModule, RouterLink],
  template: `
    @if (chips().length > 0) {
      <div class="metadata">
        <span class="label">Metadata</span>
        <ul>
          @for (chip of chips(); track chip.label) {
            <li>
              @if (chip.secretUuid) {
                <a
                  class="chip"
                  [class.flock]="chip.isFlock"
                  [routerLink]="secretLink"
                  [queryParams]="{ uuid: chip.secretUuid }"
                  [matTooltip]="'Open secret ' + chip.secretUuid"
                >
                  <mat-icon>key</mat-icon><span class="text">{{ chip.label }}</span>
                </a>
              } @else {
                <button
                  type="button"
                  class="chip"
                  [class.flock]="chip.isFlock"
                  [matTooltip]="'Copy ' + chip.label"
                  (click)="copy(chip.label); $event.stopPropagation()"
                >
                  <span class="text">{{ chip.label }}</span>
                </button>
              }
            </li>
          }
        </ul>
      </div>
    }
  `,
  styles: `
    .metadata {
      display: flex;
      align-items: baseline;
      gap: 8px;
      margin: 0 0 10px;
    }
    .label {
      flex: none;
      color: var(--mat-sys-on-surface-variant);
    }
    ul {
      display: flex;
      flex-wrap: wrap;
      gap: 4px;
      min-width: 0;
      margin: 0;
      padding: 0;
      list-style: none;
    }
    li {
      min-width: 0;
    }
    .chip {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      max-width: 48ch;
      height: 22px;
      padding: 0 9px;
      border: 1px solid transparent;
      border-radius: 12px;
      font-family: var(--flock-mono);
      font-size: 12px;
      line-height: 1;
      cursor: pointer;
      background: var(--mat-sys-surface-container-high);
      color: var(--mat-sys-on-surface);
      &:hover {
        box-shadow: inset 0 0 0 1px currentColor;
      }
      &.flock {
        border-color: var(--mat-sys-primary);
        background: transparent;
        color: var(--mat-sys-primary);
      }
      mat-icon {
        flex: none;
        color: var(--flock-warning-text);
        font-size: 14px;
        width: 14px;
        height: 14px;
      }
    }
    .text {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    // the global link style underlines and recolors every a, this selector outweighs it
    .metadata a.chip,
    .metadata a.chip:hover {
      text-decoration: none;
      color: var(--mat-sys-on-surface);
      &.flock {
        color: var(--mat-sys-primary);
      }
    }
  `,
})
export class MetadataChips {
  readonly metadata = input<Record<string, string>>();

  private clipboard = inject(Clipboard);
  private notifier = inject(Notifier);

  protected readonly secretLink = kinds.secret.link;
  protected chips = computed(() => metadataChips(this.metadata()));

  protected copy(text: string) {
    copyToClipboard(this.clipboard, this.notifier, text);
  }
}
