import { Component, inject } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatStepperModule } from '@angular/material/stepper';
import { BackendStep } from './backend-step';
import { BuilderState } from './builder-state';
import { ReviewStep } from './review-step';
import { VipStep } from './vip-step';
import { VsStep } from './vs-step';

// Builder is the Expose a service wizard of the side panel opened from the top bar and the Overview. Each run makes one virtual server with its backend
// and VIP, created on the controller or written to a new resource file for flock apply.
@Component({
  selector: 'flock-builder',
  imports: [MatIconModule, MatStepperModule, VipStep, VsStep, BackendStep, ReviewStep],
  templateUrl: './builder.html',
  styleUrl: './builder.scss',
})
export class Builder {
  protected state = inject(BuilderState);

  constructor() {
    this.state.start();
  }
}
