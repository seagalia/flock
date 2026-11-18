import { Component, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { RouterLink } from '@angular/router';

export interface ErrorDialogData {
  title: string;
  message: string;
  // resources the message names, each shown in the table of its page on click
  links?: { label: string; path: string; uuid: string }[];
}

@Component({
  selector: 'flock-error-dialog',
  imports: [MatDialogModule, MatButtonModule, MatIconModule, RouterLink],
  template: `
    <h2 mat-dialog-title>{{ data.title }}</h2>
    <mat-dialog-content class="content">
      <mat-icon class="icon">error</mat-icon>
      <div>
        <span>{{ data.message }}</span>
        @if (data.links?.length) {
          <ul>
            @for (link of data.links; track link.uuid) {
              <li>
                <a [routerLink]="link.path" [queryParams]="{ uuid: link.uuid }" mat-dialog-close>{{
                  link.label
                }}</a>
              </li>
            }
          </ul>
        }
      </div>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button mat-dialog-close class="ok">Ok</button>
    </mat-dialog-actions>
  `,
  styles: `
    .content {
      display: flex;
      gap: 8px;
      align-items: flex-start;
    }
    .icon,
    .ok {
      color: var(--flock-error);
    }
  `,
})
export class ErrorDialog {
  protected data = inject<ErrorDialogData>(MAT_DIALOG_DATA);
}
