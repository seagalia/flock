import { computed, DOCUMENT, inject, Injectable, signal } from '@angular/core';

export type ThemeChoice = 'system' | 'light' | 'dark';

const storageKey = 'flock_theme';

// Theme follows the system color scheme unless the user picks one, the choice is kept per browser
@Injectable({ providedIn: 'root' })
export class Theme {
  private document = inject(DOCUMENT);
  private systemDark = signal(false);

  readonly choice = signal<ThemeChoice>(this.loadChoice());
  readonly isDark = computed(() => {
    const choice = this.choice();
    return choice === 'dark' || (choice === 'system' && this.systemDark());
  });

  constructor() {
    const query = this.document.defaultView?.matchMedia('(prefers-color-scheme: dark)');
    if (query) {
      this.systemDark.set(query.matches);
      query.addEventListener('change', (event) => this.systemDark.set(event.matches));
    }
    this.apply(this.choice());
  }

  toggle() {
    const next: ThemeChoice = this.isDark() ? 'light' : 'dark';
    this.choice.set(next);
    this.apply(next);
    try {
      localStorage.setItem(storageKey, next);
    } catch {
      // the choice only lasts for this page when the browser blocks storage
    }
  }

  // apply sets color-scheme on <html>: the material and flock tokens are light-dark() values
  private apply(choice: ThemeChoice) {
    const root = this.document.documentElement;
    root.classList.remove('theme-light', 'theme-dark');
    if (choice !== 'system') {
      root.classList.add(`theme-${choice}`);
    }
  }

  private loadChoice(): ThemeChoice {
    try {
      const stored = localStorage.getItem(storageKey);
      if (stored === 'light' || stored === 'dark') {
        return stored;
      }
    } catch {
      // storage blocked: follow the system
    }
    return 'system';
  }
}
