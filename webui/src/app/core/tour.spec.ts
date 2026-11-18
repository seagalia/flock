import { TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';
import { Theme } from './theme';
import { Tour } from './tour';

describe('Tour offer', () => {
  let open: ReturnType<typeof vi.fn>;

  function setup(answer: boolean) {
    open = vi.fn(() => ({ afterClosed: () => of(answer) }));
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: MatDialog, useValue: { open } },
        // jsdom has no matchMedia for the theme to follow
        { provide: Theme, useValue: { isDark: () => false } },
      ],
    });
    const tour = TestBed.inject(Tour);
    const start = vi.spyOn(tour, 'start').mockResolvedValue(undefined);
    return { tour, start };
  }

  beforeEach(() => localStorage.clear());

  it('stays quiet when the login page was not asked to offer it', async () => {
    const { tour } = setup(true);

    await tour.offerOnce();

    expect(open).not.toHaveBeenCalled();
  });

  it('starts the tour on yes and keeps the answer', async () => {
    const { tour, start } = setup(true);
    tour.askOffer();

    await tour.offerOnce();

    expect(start).toHaveBeenCalled();
    expect(localStorage.getItem('flock_tour')).toBe('started');
  });

  it('keeps a no without starting the tour', async () => {
    const { tour, start } = setup(false);
    tour.askOffer();

    await tour.offerOnce();

    expect(start).not.toHaveBeenCalled();
    expect(localStorage.getItem('flock_tour')).toBe('declined');
  });

  it('never asks again once the browser holds an answer', async () => {
    localStorage.setItem('flock_tour', 'declined');
    const { tour } = setup(true);
    tour.askOffer();

    await tour.offerOnce();

    expect(open).not.toHaveBeenCalled();
  });
});
