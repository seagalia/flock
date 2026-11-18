import { Component, input, output } from '@angular/core';
import { MatTooltipModule } from '@angular/material/tooltip';

export interface Choice {
  value: string;
  title: string;
  line: string;
  // why the choice can't be picked, empty when it can
  greyed?: string;
}

// ChoiceCards picks one of a few choices shown as cards, each with a line saying what it is for. It is used where a
// select would hide what each choice means, such as the protocol of a virtual server or the case of a oneof.
@Component({
  selector: 'flock-choice-cards',
  imports: [MatTooltipModule],
  template: `
    <div class="cards" role="group" [attr.aria-label]="label()">
      @for (choice of choices(); track choice.value) {
        <button
          type="button"
          class="card"
          [attr.data-choice]="choice.value"
          [class.selected]="value() === choice.value"
          [attr.aria-pressed]="value() === choice.value"
          [disabled]="disabled() || !!choice.greyed"
          [matTooltip]="choice.greyed ?? ''"
          (click)="picked.emit(choice.value)"
        >
          <span class="card-title">{{ choice.title }}</span>
          @if (choice.line) {
            <span class="card-line">{{ choice.line }}</span>
          }
        </button>
      }
    </div>
  `,
  styles: `
    .cards {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
    }
    .card {
      display: flex;
      flex-direction: column;
      align-items: flex-start;
      gap: 4px;
      min-width: 120px;
      max-width: 200px;
      padding: 12px;
      border: 1px solid var(--flock-border);
      border-radius: 12px;
      background: var(--mat-sys-surface);
      color: var(--mat-sys-on-surface);
      font: inherit;
      text-align: left;
      cursor: pointer;
      &:hover:not(:disabled) {
        border-color: var(--mat-sys-primary);
      }
      &.selected {
        border-color: var(--mat-sys-primary);
        background: var(--mat-sys-secondary-container);
        color: var(--mat-sys-on-secondary-container);
      }
      &:disabled {
        cursor: default;
        opacity: 0.6;
      }
      &.selected:disabled {
        opacity: 1;
      }
    }
    .card-title {
      font-weight: 600;
    }
    .card-line {
      font-size: 0.85rem;
    }
  `,
})
export class ChoiceCards {
  readonly choices = input.required<Choice[]>();
  readonly value = input('');
  readonly label = input('');
  readonly disabled = input(false);
  readonly picked = output<string>();
}
