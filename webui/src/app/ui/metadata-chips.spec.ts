import { Clipboard } from '@angular/cdk/clipboard';
import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { Notifier } from '../core/notifier';
import { MetadataChips, metadataChips } from './metadata-chips';

@Component({
  imports: [MetadataChips],
  template: `<flock-metadata-chips [metadata]="metadata()" />`,
})
class Host {
  metadata = signal<Record<string, string> | undefined>(undefined);
}

const secretUuid = '0f8e5c1a-3b7d-4e2a-9c6f-1d2e3f4a5b6c';

async function render(metadata: Record<string, string> | undefined): Promise<HTMLElement> {
  const fixture = TestBed.createComponent(Host);
  fixture.componentInstance.metadata.set(metadata);
  await fixture.whenStable();
  return fixture.nativeElement;
}

describe('MetadataChips', () => {
  const copied: string[] = [];

  beforeEach(() => {
    copied.length = 0;
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: Clipboard, useValue: { copy: (text: string) => copied.push(text) > 0 } },
        { provide: Notifier, useValue: { info: () => undefined } },
      ],
    });
  });

  it('lists the own keys first, then the flock keys, each sorted', () => {
    const chips = metadataChips({
      'flock.provision.by': 'p1',
      team: 'web',
      'flock.acme.failures': '2',
      env: 'prod',
    });

    expect(chips.map((chip) => [chip.label, chip.isFlock])).toEqual([
      ['env=prod', false],
      ['team=web', false],
      ['flock.acme.failures=2', true],
      ['flock.provision.by=p1', true],
    ]);
  });

  it('tints the flock keys', async () => {
    const element = await render({ team: 'web', 'flock.acme.failures': '2' });

    const chips = Array.from(element.querySelectorAll('.chip'));
    expect(chips.map((chip) => chip.textContent?.trim())).toEqual([
      'team=web',
      'flock.acme.failures=2',
    ]);
    expect(chips.map((chip) => chip.classList.contains('flock'))).toEqual([false, true]);
  });

  it('copies the key and the value of a chip on click', async () => {
    const element = await render({ team: 'web' });

    element.querySelector<HTMLButtonElement>('button.chip')!.click();

    expect(copied).toEqual(['team=web']);
  });

  it('links a secret reference to the secret in the secrets page', async () => {
    const element = await render({ token: `secret://${secretUuid}/api_key` });

    const link = element.querySelector<HTMLAnchorElement>('a.chip')!;
    expect(link.getAttribute('href')).toBe(`/secrets?uuid=${secretUuid}`);
    expect(element.querySelector('button.chip')).toBeNull();
  });

  it('shows nothing for a resource with no metadata', async () => {
    expect((await render({})).querySelector('.metadata')).toBeNull();
    expect((await render(undefined)).querySelector('.metadata')).toBeNull();
  });
});
