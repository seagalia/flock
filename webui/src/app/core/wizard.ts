import { Injectable, signal } from '@angular/core';

// Wizard opens the Expose a service panel of the shell from any page. It is kept apart from BuilderState so that a
// page with the button doesn't load the wizard and its yaml parser.
@Injectable({ providedIn: 'root' })
export class Wizard {
  readonly isOpen = signal(false);
}
