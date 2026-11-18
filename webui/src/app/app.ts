import { Component, inject } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { Theme } from './core/theme';

@Component({
  selector: 'flock-root',
  imports: [RouterOutlet],
  template: '<router-outlet />',
})
export class App {
  // the theme applies the saved color scheme before the first page shows
  private theme = inject(Theme);
}
