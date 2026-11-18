import {
  afterNextRender,
  Component,
  computed,
  DOCUMENT,
  ElementRef,
  inject,
  Injectable,
  input,
  OnDestroy,
  output,
  signal,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDividerModule } from '@angular/material/divider';
import { MatIconModule } from '@angular/material/icon';

const slideMs = 300;

// a form wider than this spreads its labels and inputs too far apart on a big screen
const maxPanelPx = 1100;
// each panel opened from another one is this much narrower, so the edge of the one below stays visible to click
const stackStepPx = 40;

// What SidePanels needs from an open panel to close it from below
interface PanelHandle {
  // resolves true once the panel is closed, false when its canClose refused
  close(): Promise<boolean>;
}

// SidePanels is the stack of the open panels. A panel opened from another one is narrower and above it.
@Injectable({ providedIn: 'root' })
export class SidePanels {
  private stack: PanelHandle[] = [];

  push(handle: PanelHandle): number {
    this.stack.push(handle);
    return this.stack.length;
  }

  pop(handle: PanelHandle) {
    this.stack = this.stack.filter((open) => open !== handle);
  }

  isTop(level: number): boolean {
    return level === this.stack.length;
  }

  // Closes the panels above the given level, the top one first, each through its own canClose. A refusal stops
  // the sequence so that a panel with unsaved changes is never skipped.
  async closeAbove(level: number): Promise<boolean> {
    // a snapshot, because a closed panel leaves the stack only when its parent has removed it
    const above = this.stack.slice(level).reverse();
    for (const panel of above) {
      if (!(await panel.close())) {
        return false;
      }
    }
    return true;
  }
}

// SidePanel slides from the right over the page, it closes on its close button, its Ok button or the backdrop,
// each asking canClose first. The parent renders it to open it and removes it on (closed). Content projected with
// [panelHeader] sits next to the title, content with [panelActions] replaces the Ok button. A [wide] panel takes
// most of the window whatever its size, for content that uses the room like the config map.
@Component({
  selector: 'flock-side-panel',
  host: { '(document:keydown.escape)': 'closeIfTop($event)' },
  imports: [MatButtonModule, MatIconModule, MatDividerModule],
  template: `
    <div
      class="backdrop"
      [class.shown]="shown()"
      [style.z-index]="zIndex - 1"
      (click)="close()"
      (keydown.escape)="close()"
      tabindex="-1"
    ></div>
    <aside
      class="panel"
      [class.shown]="shown()"
      [style.width]="width()"
      [style.z-index]="zIndex"
      role="dialog"
      [attr.aria-label]="title()"
    >
      <div class="panel-control">
        <button mat-fab class="close" aria-label="Close" (click)="close()">
          <mat-icon>close</mat-icon>
        </button>
      </div>
      <div class="panel-body">
        <h2 class="title">
          {{ title() }}
          @if (subject()) {
            <span class="subject">{{ subject() }}</span>
          }
          <span class="header-actions"><ng-content select="[panelHeader]" /></span>
        </h2>
        <mat-divider />
        <div class="content"><ng-content /></div>
        <mat-divider />
        <div class="actions">
          <ng-content select="[panelActions]">
            <button mat-flat-button (click)="close()">
              <mat-icon>done</mat-icon>
              Ok
            </button>
          </ng-content>
        </div>
      </div>
    </aside>
  `,
  styles: `
    .backdrop {
      position: fixed;
      inset: 0;
      background: transparent;
      transition: background-color ${slideMs}ms;
      &.shown {
        background: var(--flock-backdrop);
      }
    }
    .panel {
      position: fixed;
      top: 0;
      bottom: 0;
      right: 0;
      max-width: 100vw;
      display: flex;
      background: var(--mat-sys-surface);
      border-left: 1px solid var(--flock-border);
      transform: translateX(100%);
      transition: transform ${slideMs}ms ease-out;
      &.shown {
        transform: translateX(0);
      }
    }
    .panel-control {
      padding: 15px 0 0 15px;
    }
    .close {
      background: var(--mat-sys-tertiary);
      color: var(--mat-sys-on-tertiary);
    }
    .panel-body {
      flex: 1;
      min-width: 0;
      display: flex;
      flex-direction: column;
      padding: 15px;
      height: calc(100vh - 30px);
    }
    .title {
      display: flex;
      align-items: center;
      gap: 8px;
    }
    .header-actions {
      margin-left: auto;
    }
    .subject {
      color: var(--mat-sys-primary);
    }
    .content {
      flex: 1;
      overflow-y: auto;
      margin: 15px 0;
    }
    .actions {
      display: flex;
      gap: 8px;
      padding-top: 15px;
    }
    @media (max-width: 600px) {
      .panel {
        width: 100% !important;
      }
      .panel-control {
        position: absolute;
        right: 15px;
      }
    }
  `,
})
export class SidePanel implements OnDestroy {
  readonly title = input.required<string>();
  readonly subject = input<string>('');
  // Asked before every close (button, Escape, backdrop). A page with unsaved changes answers false after asking.
  readonly canClose = input<() => Promise<boolean>>(async () => true);
  readonly closed = output<void>();
  readonly wide = input(false);

  private panels = inject(SidePanels);
  private handle: PanelHandle = { close: () => this.close() };
  private level = this.panels.push(this.handle);
  protected shown = signal(false);
  protected readonly width = computed(() => {
    if (this.wide()) {
      return `${93 - this.level}vw`;
    }
    const stackOffsetPx = (this.level - 1) * stackStepPx;
    return `calc(min(92vw, ${maxPanelPx}px) - ${stackOffsetPx}px)`;
  });
  protected readonly zIndex = 1000 + this.level * 2;
  private closing = false;
  private asking = false;

  private host = inject<ElementRef<HTMLElement>>(ElementRef).nativeElement;

  constructor() {
    const body = inject(DOCUMENT).body;
    afterNextRender(() => {
      // above the shell: the toolbar and the sidenav are stacking contexts the panel can't escape from inside
      body.appendChild(this.host);
      // rendered hidden first, the slide starts on the next frame
      requestAnimationFrame(() => this.shown.set(true));
    });
  }

  ngOnDestroy() {
    this.panels.pop(this.handle);
    this.host.remove();
  }

  // isTop tells whether no panel was opened above this one, content reacting to Escape checks it
  isTop(): boolean {
    return this.panels.isTop(this.level);
  }

  // Escape closes the panel opened last only, the ones below stay open. An Escape a capture listener
  // already handled (and prevented) belongs to it, the panel stays.
  protected closeIfTop(event: Event) {
    if (event.defaultPrevented) {
      return;
    }
    if (this.panels.isTop(this.level)) {
      this.close();
    }
  }

  // Closes the panels above this one first, then asks canClose. Resolves true once the panel has slid out and
  // emitted closed, false when a canClose refused. A panel already closing counts as closed.
  async close(): Promise<boolean> {
    if (this.closing) {
      return true;
    }
    const aboveClosed = await this.panels.closeAbove(this.level);
    if (!aboveClosed) {
      return false;
    }
    // asked once at a time, so a second click while the question is shown doesn't stack a second question
    if (this.asking) {
      return false;
    }
    this.asking = true;
    // a failing question counts as a refusal, because closing could lose changes the page could not check
    const allowed = await this.canClose()()
      .catch(() => false)
      .finally(() => (this.asking = false));
    if (!allowed) {
      return false;
    }
    this.closing = true;
    this.shown.set(false);
    await new Promise<void>((resolve) =>
      setTimeout(() => {
        this.closed.emit();
        resolve();
      }, slideMs),
    );
    return true;
  }
}
