import { Component, computed, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialog, MatDialogModule } from '@angular/material/dialog';
import { firstValueFrom } from 'rxjs';

export interface ConfirmData {
  title: string;
  message: string;
  confirm: string;
  // the label of the button that says no, Cancel by default
  cancel?: string;
  // listed under the message, the steps of an action doing several things
  items?: string[];
  // the text the user types before the confirm button turns on, for an action hard to take back
  typed?: string;
}

@Component({
  selector: 'flock-confirm-dialog',
  imports: [MatButtonModule, MatDialogModule],
  template: `
    <h2 mat-dialog-title>{{ data.title }}</h2>
    <mat-dialog-content>
      <p>{{ data.message }}</p>
      @if (data.items?.length) {
        <ul>
          @for (item of data.items; track $index) {
            <li>{{ item }}</li>
          }
        </ul>
      }
      @if (data.typed) {
        <label class="typed-label">
          Type <strong>{{ data.typed }}</strong> to confirm
          <input
            class="typed"
            autocomplete="off"
            spellcheck="false"
            (input)="typedValue.set($any($event.target).value)"
          />
        </label>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button type="button" [mat-dialog-close]="false">
        {{ data.cancel ?? 'Cancel' }}
      </button>
      <button mat-flat-button type="button" [disabled]="typedMissing()" [mat-dialog-close]="true">
        {{ data.confirm }}
      </button>
    </mat-dialog-actions>
  `,
  styles: `
    .typed-label {
      display: flex;
      flex-direction: column;
      gap: 6px;
    }
    .typed {
      font: inherit;
      font-family: var(--flock-mono);
      padding: 8px;
      border: 1px solid var(--mat-sys-outline);
      border-radius: 4px;
      background: transparent;
      color: inherit;
    }
  `,
})
export class ConfirmDialog {
  protected data = inject<ConfirmData>(MAT_DIALOG_DATA);
  protected readonly typedValue = signal('');
  protected readonly typedMissing = computed(
    () => !!this.data.typed && this.typedValue() !== this.data.typed,
  );
}

// confirm resolves true only when the user picked the confirm button, a dismissed dialog is a no
export async function confirm(dialog: MatDialog, data: ConfirmData): Promise<boolean> {
  const answer = await firstValueFrom(dialog.open(ConfirmDialog, { data }).afterClosed());
  return answer === true;
}
