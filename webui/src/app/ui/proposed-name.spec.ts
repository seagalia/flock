import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { ProposedName } from './proposed-name';

@Component({
  imports: [ProposedName],
  template: `<input
    flockProposedName
    [value]="value"
    (input)="typed.push($any($event.target).value)"
  />`,
})
class Host {
  value = 'hoopoe-cassis';
  typed: string[] = [];
}

function press(input: HTMLInputElement, key: string) {
  const event = new KeyboardEvent('keydown', { key, cancelable: true });
  input.dispatchEvent(event);
  return event;
}

describe('ProposedName', () => {
  it('clears a proposed name on the first Backspace, and tells the form', async () => {
    const fixture = TestBed.createComponent(Host);
    await fixture.whenStable();
    const input = fixture.nativeElement.querySelector('input') as HTMLInputElement;

    expect(press(input, 'Backspace').defaultPrevented).toBe(true);
    expect(input.value).toBe('');
    expect(fixture.componentInstance.typed).toEqual(['']);
  });

  it('leaves a name the user typed to the browser, and every other key', async () => {
    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.value = 'web';
    await fixture.whenStable();
    const input = fixture.nativeElement.querySelector('input') as HTMLInputElement;

    expect(press(input, 'Delete').defaultPrevented).toBe(false);
    input.value = 'hoopoe-cassis';
    expect(press(input, 'a').defaultPrevented).toBe(false);
    expect(fixture.componentInstance.typed).toEqual([]);
  });
});
