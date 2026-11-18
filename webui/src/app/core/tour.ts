import { inject, Injectable } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Router } from '@angular/router';
import type { DriveStep } from 'driver.js';
import { confirm } from '../ui/confirm-dialog';
import { Theme } from './theme';
import { Wizard } from './wizard';

// the answer to the offer, kept per browser so the offer comes once
const decisionKey = 'flock_tour';

// tourSteps follow the dashboard reference of the docs, an element missing from the page (an empty instance, a
// narrow screen hiding the menu) shows its popover in the middle of the page
export const tourSteps: DriveStep[] = [
  {
    popover: {
      title: 'Welcome to the Flock dashboard',
      description:
        'This tour shows where things are. Move with the arrow keys or the buttons, and leave with Escape.',
    },
  },
  {
    element: '[data-tour="instance"]',
    popover: {
      title: 'Pick an instance',
      description:
        'Most pages work on one instance. The overview lists the virtual servers it serves, with the health of their endpoints.',
    },
  },
  {
    element: '[data-tour="overview"]',
    popover: {
      title: 'Your virtual servers',
      description:
        'Select a row to see its backends and their endpoints. Delete, in the ⋮ menu of a row, removes a virtual server with the backends only it uses, after a confirmation that lists each step.',
    },
  },
  {
    element: '[data-tour="config-map"]',
    popover: {
      title: 'Config map',
      description:
        'This button draws the path of a request, from the VIPs of the virtual server to the endpoints of its backends. Click a node to see its summary, edit it or delete it.',
    },
  },
  {
    element: '[data-tour="expose"]',
    popover: {
      title: 'Expose a service',
      description:
        'The wizard makes a virtual server and its backend, and adds the virtual server to a VIP. It creates them on the controller, or writes them to a file for flock apply.',
    },
  },
  {
    element: '[data-tour="nav"]',
    popover: {
      title: 'One page per resource',
      description:
        'Each kind of resource has its page, with a Create button above its table. The ⋮ menu of a row edits, deletes or clones the resource, and an expanded row repeats Edit and Delete.',
    },
  },
  {
    element: '[data-tour="Status"]',
    popover: {
      title: 'Status',
      description:
        'What the agents of an instance did with each resource. A resource an agent refused shows its error message.',
    },
  },
  {
    element: '[data-tour="Monitors"]',
    popover: {
      title: 'Monitors',
      description: 'The logs of the agents of an instance, streamed live in a terminal.',
    },
  },
  {
    element: '[data-tour="theme"]',
    popover: {
      title: 'Light or dark',
      description: 'Switch the theme. This browser keeps your choice.',
    },
  },
  {
    element: '[data-tour="tour"]',
    popover: {
      title: 'Take the tour again',
      description: 'This button starts the tour again whenever you want.',
    },
  },
  {
    element: '[data-tour="user"]',
    popover: {
      title: 'Your account',
      description: 'The menu of your account shows your groups and signs you out.',
    },
  },
];

// the bird of the last step flies off on Done, then the tour closes. Long enough for the flight of styles.scss.
const flightMs = 1400;

function finaleStep(birdSrc: string): DriveStep {
  return {
    popover: {
      title: 'You are all set',
      description: `<img class="flock-tour-bird" src="${birdSrc}" alt="" width="96" height="90" />The tour ends here. The flag of the top bar starts it again.`,
    },
  };
}

// flyAway lets the bird of the last popover leave the page before the tour closes, at once for a user who asks the
// system for reduced motion
function flyAway(done: () => void) {
  const bird = document.querySelector('.flock-tour-bird');
  const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  if (!bird || reducedMotion) {
    done();
    return;
  }
  bird.classList.add('flying');
  setTimeout(done, flightMs);
}

// Tour is the guided tour of the dashboard. flock dev asks for its offer in the fragment of the login page, the
// offer then shows once in the shell after the login, and the top bar starts the tour at any time.
@Injectable({ providedIn: 'root' })
export class Tour {
  private router = inject(Router);
  private dialog = inject(MatDialog);
  private wizard = inject(Wizard);
  private theme = inject(Theme);
  private offerAsked = false;

  askOffer() {
    this.offerAsked = true;
  }

  async offerOnce() {
    if (!this.offerAsked || this.decision()) {
      return;
    }
    this.offerAsked = false;
    const wantsTour = await confirm(this.dialog, {
      title: 'Take the tour?',
      message: 'A short tour shows the pages of the dashboard and where to find each action.',
      confirm: 'Start the tour',
      cancel: 'No thanks',
    });
    this.keepDecision(wantsTour ? 'started' : 'declined');
    if (wantsTour) {
      await this.start();
    }
  }

  // the tour runs on the overview, whose elements it points at, with no side panel over it
  async start() {
    this.wizard.isOpen.set(false);
    await this.router.navigateByUrl('/');
    // imported on start so the tour stays out of the initial bundle
    const { driver } = await import('driver.js');
    const birdSrc = this.theme.isDark() ? 'brand/symbol-dark.svg' : 'brand/symbol.svg';
    const tour = driver({
      steps: [...tourSteps, finaleStep(birdSrc)],
      showProgress: true,
      popoverClass: 'flock-tour',
      nextBtnText: 'Next',
      prevBtnText: 'Back',
      doneBtnText: 'Done',
      // Done, Escape and the close button all end here, only the end of the last step waits for the flight
      onDestroyStarted: () => {
        if (!tour.isLastStep()) {
          tour.destroy();
          return;
        }
        flyAway(() => tour.destroy());
      },
    });
    tour.drive();
  }

  private decision(): string | null {
    try {
      return localStorage.getItem(decisionKey);
    } catch {
      return null;
    }
  }

  private keepDecision(decision: 'started' | 'declined') {
    try {
      localStorage.setItem(decisionKey, decision);
    } catch {
      // the browser blocks storage, the offer comes back at the next login of flock dev
    }
  }
}
