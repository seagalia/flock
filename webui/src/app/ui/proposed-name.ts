import { Directive, ElementRef, inject } from '@angular/core';
import { isProposedName } from '../core/names';

// ProposedName clears a proposed name at once on the first Backspace or Delete, since half of one means nothing. A
// name the user typed, or changed, is edited key by key as usual.
@Directive({
  selector: 'input[flockProposedName]',
  host: { '(keydown)': 'onKeydown($event)' },
})
export class ProposedName {
  private input = inject<ElementRef<HTMLInputElement>>(ElementRef).nativeElement;

  protected onKeydown(event: KeyboardEvent) {
    const isErase = event.key === 'Backspace' || event.key === 'Delete';
    if (!isErase || !isProposedName(this.input.value)) {
      return;
    }
    event.preventDefault();
    this.input.value = '';
    // the forms and ngModel listen to input events, a value set by code sends none
    this.input.dispatchEvent(new Event('input', { bubbles: true }));
  }
}
