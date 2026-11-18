import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { confirm } from './confirm-dialog';

describe('confirm', () => {
  afterEach(() => {
    document.querySelectorAll('.cdk-overlay-container').forEach((e) => e.remove());
  });

  function confirmButton(): HTMLButtonElement {
    const buttons = Array.from(
      document.querySelectorAll<HTMLButtonElement>('mat-dialog-actions button'),
    );
    return buttons.find((button) => button.textContent?.includes('Delete'))!;
  }

  async function settle() {
    await new Promise((resolve) => setTimeout(resolve));
    TestBed.tick();
  }

  it('keeps the confirm button off until the typed text matches', async () => {
    const dialog = TestBed.inject(MatDialog);
    const answer = confirm(dialog, {
      title: 'Delete the locked backend auto?',
      message: 'Its provisioner may create it again.',
      confirm: 'Delete',
      typed: 'auto',
    });
    await settle();

    expect(confirmButton().disabled).toBe(true);
    const input = document.querySelector<HTMLInputElement>('input.typed')!;
    input.value = 'aut';
    input.dispatchEvent(new Event('input'));
    await settle();
    expect(confirmButton().disabled).toBe(true);

    input.value = 'auto';
    input.dispatchEvent(new Event('input'));
    await settle();
    expect(confirmButton().disabled).toBe(false);
    confirmButton().click();
    expect(await answer).toBe(true);
  });

  it('needs nothing typed when no text is asked', async () => {
    const dialog = TestBed.inject(MatDialog);
    void confirm(dialog, { title: 'Delete the backend web?', message: '', confirm: 'Delete' });
    await settle();

    expect(document.querySelector('input.typed')).toBeNull();
    expect(confirmButton().disabled).toBe(false);
  });
});
