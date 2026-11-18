import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { SidePanel } from './side-panel';

@Component({
  imports: [SidePanel],
  template: `<flock-side-panel title="Panel" (closed)="closedCount = closedCount + 1" />`,
})
class Host {
  closedCount = 0;
}

@Component({
  imports: [SidePanel],
  template: `
    <flock-side-panel title="Panel" [canClose]="canClose" (closed)="closedCount = closedCount + 1">
      <button panelActions class="save">Save</button>
    </flock-side-panel>
  `,
})
class DirtyHost {
  closedCount = 0;
  canClose = vi.fn(async () => false);
}

@Component({
  imports: [SidePanel],
  template: `
    <flock-side-panel title="Lower" [canClose]="lowerCanClose" (closed)="closes.push('lower')">
      @if (upperOpen()) {
        <flock-side-panel
          title="Upper"
          [canClose]="upperCanClose"
          (closed)="closes.push('upper'); upperOpen.set(false)"
        />
      }
    </flock-side-panel>
  `,
})
class StackedHost {
  closes: string[] = [];
  upperOpen = signal(true);
  lowerCanClose = vi.fn(async () => true);
  upperCanClose = vi.fn(async () => true);
}

@Component({
  imports: [SidePanel],
  template: `<flock-side-panel title="Map" [wide]="true" />`,
})
class WideHost {}

function panelWidth(title: string): string {
  return document.querySelector<HTMLElement>(`aside.panel[aria-label="${title}"]`)!.style.width;
}

// the DOM rewrites a calc() in its own form, the expected value goes through the same rewriting
function asStyled(width: string): string {
  const probe = document.createElement('div');
  probe.style.width = width;
  return probe.style.width;
}

function pressEscape() {
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true }));
}

describe('SidePanel', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  async function render() {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);
    return fixture;
  }

  it('caps its width and makes a stacked panel narrower', async () => {
    const fixture = TestBed.createComponent(StackedHost);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);

    expect(panelWidth('Lower')).toBe(asStyled('calc(min(92vw, 1100px) - 0px)'));
    expect(panelWidth('Upper')).toBe(asStyled('calc(min(92vw, 1100px) - 40px)'));
    fixture.destroy();
  });

  it('takes most of the window when wide', async () => {
    const fixture = TestBed.createComponent(WideHost);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);

    expect(panelWidth('Map')).toBe('92vw');
    fixture.destroy();
  });

  it('closes on escape', async () => {
    const fixture = await render();

    pressEscape();
    await vi.advanceTimersByTimeAsync(300);

    expect(fixture.componentInstance.closedCount).toBe(1);
    fixture.destroy();
  });

  it('ignores an escape already handled', async () => {
    const fixture = await render();
    const handle = (event: Event) => event.preventDefault();
    document.addEventListener('keydown', handle, { capture: true });

    pressEscape();
    await vi.advanceTimersByTimeAsync(300);
    document.removeEventListener('keydown', handle, { capture: true });

    expect(fixture.componentInstance.closedCount).toBe(0);
    fixture.destroy();
  });

  it('asks before closing a dirty panel', async () => {
    const fixture = TestBed.createComponent(DirtyHost);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);

    pressEscape();
    await vi.advanceTimersByTimeAsync(300);
    (document.querySelector('.backdrop') as HTMLElement).click();
    await vi.advanceTimersByTimeAsync(300);
    (document.querySelector('button.close') as HTMLElement).click();
    await vi.advanceTimersByTimeAsync(300);

    expect(fixture.componentInstance.canClose).toHaveBeenCalledTimes(3);
    expect(fixture.componentInstance.closedCount).toBe(0);
    fixture.destroy();
  });

  it('keeps the panel open when canClose rejects', async () => {
    const fixture = TestBed.createComponent(DirtyHost);
    fixture.componentInstance.canClose.mockRejectedValue(new Error('boom'));
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);

    pressEscape();
    await vi.advanceTimersByTimeAsync(300);

    expect(fixture.componentInstance.closedCount).toBe(0);
    fixture.destroy();
  });

  it('closes once canClose agrees', async () => {
    const fixture = TestBed.createComponent(DirtyHost);
    fixture.componentInstance.canClose.mockResolvedValue(true);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);

    pressEscape();
    await vi.advanceTimersByTimeAsync(300);

    expect(fixture.componentInstance.closedCount).toBe(1);
    fixture.destroy();
  });

  it('replaces the Ok button with the projected actions', async () => {
    const fixture = TestBed.createComponent(DirtyHost);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);

    const actions = document.querySelector('.actions') as HTMLElement;
    expect(actions.querySelector('button.save')).not.toBeNull();
    expect(actions.textContent).not.toContain('Ok');
    fixture.destroy();
  });

  it('keeps the Ok button without projected actions', async () => {
    const fixture = await render();

    expect((document.querySelector('.actions') as HTMLElement).textContent).toContain('Ok');
    fixture.destroy();
  });

  it('escape closes only the top panel', async () => {
    const fixture = TestBed.createComponent(StackedHost);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);

    pressEscape();
    await vi.advanceTimersByTimeAsync(300);

    expect(fixture.componentInstance.closes).toEqual(['upper']);
    expect(fixture.componentInstance.lowerCanClose).not.toHaveBeenCalled();
    fixture.destroy();
  });

  it('closes the panels above a lower one first, each through its canClose', async () => {
    const fixture = TestBed.createComponent(StackedHost);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);

    const lowerBackdrop = document.querySelector(
      'flock-side-panel[title="Lower"] > .backdrop',
    ) as HTMLElement;
    lowerBackdrop.click();
    await vi.advanceTimersByTimeAsync(300);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(300);

    expect(fixture.componentInstance.upperCanClose).toHaveBeenCalledTimes(1);
    expect(fixture.componentInstance.lowerCanClose).toHaveBeenCalledTimes(1);
    expect(fixture.componentInstance.closes).toEqual(['upper', 'lower']);
    fixture.destroy();
  });

  it('keeps the lower panel open when the one above refuses', async () => {
    const fixture = TestBed.createComponent(StackedHost);
    fixture.componentInstance.upperCanClose.mockResolvedValue(false);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);
    fixture.detectChanges();
    await vi.advanceTimersByTimeAsync(0);

    (document.querySelector('flock-side-panel[title="Lower"] > .backdrop') as HTMLElement).click();
    await vi.advanceTimersByTimeAsync(600);

    expect(fixture.componentInstance.closes).toEqual([]);
    expect(fixture.componentInstance.lowerCanClose).not.toHaveBeenCalled();
    fixture.destroy();
  });
});
