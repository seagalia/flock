import { Component, computed, input } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { isLocked, lockReason } from './lock';

// LockBadge marks a locked resource in the name cell of its row, it shows nothing for the others
@Component({
  selector: 'flock-lock-badge',
  imports: [MatIconModule, MatTooltipModule],
  template: `
    @if (locked()) {
      <mat-icon class="lock" aria-label="Locked" [matTooltip]="lockReason">lock</mat-icon>
    }
  `,
  styles: `
    .lock {
      font-size: 16px;
      width: 16px;
      height: 16px;
      vertical-align: text-bottom;
      margin-left: 4px;
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class LockBadge {
  readonly resource = input.required<{ metadata?: Record<string, string> }>();
  protected readonly locked = computed(() => isLocked(this.resource()));
  protected readonly lockReason = lockReason;
}
