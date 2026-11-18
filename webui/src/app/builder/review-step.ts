import { Clipboard } from '@angular/cdk/clipboard';
import { Component, computed, effect, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatTooltipModule } from '@angular/material/tooltip';
import { CliHost, FileProblem } from '../core/cli-host';
import { Notifier } from '../core/notifier';
import { confirm } from '../ui/confirm-dialog';
import { CopyButton } from '../ui/copy-button';
import { BuilderState, newFileName, steps } from './builder-state';
import { downloadFile } from '../core/download';

// ReviewStep ends the run in one of two ways. It creates the resources on the controller, or it generates a new file
// for flock apply, saved by the cli or copied and downloaded on a controller.
@Component({
  selector: 'flock-review-step',
  imports: [
    CopyButton,
    FormsModule,
    MatButtonModule,
    MatButtonToggleModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatTooltipModule,
  ],
  template: `
    <mat-button-toggle-group
      name="output"
      aria-label="Outcome of the run"
      [value]="state.effectiveOutput()"
      [disabled]="isFinished()"
      (change)="state.output.set($event.value)"
    >
      <mat-button-toggle value="create">Create the resources</mat-button-toggle>
      <mat-button-toggle
        value="file"
        disabledInteractive
        [disabled]="state.fileGreyed() !== ''"
        [matTooltip]="state.fileGreyed()"
      >
        Generate a file
      </mat-button-toggle>
    </mat-button-toggle-group>

    @if (state.outcome(); as outcome) {
      <div class="outcome" role="status">
        <section>
          <h3>Created:</h3>
          <ul>
            @for (label of outcome.created; track label) {
              <li>{{ label }}</li>
            } @empty {
              <li class="note">nothing</li>
            }
          </ul>
        </section>
        @if (outcome.notCreated.length > 0) {
          <section>
            <h3>Not created:</h3>
            <ul>
              @for (label of outcome.notCreated; track label) {
                <li>{{ label }}</li>
              }
            </ul>
          </section>
        }
      </div>
      @if (outcome.error) {
        <p class="problem" role="alert">
          <mat-icon>error</mat-icon>
          {{ outcome.error }}
        </p>
      }
    } @else if (!state.runDone()) {
      <p class="note">An earlier step still has a problem to fix.</p>
    } @else if (state.effectiveOutput() === 'create') {
      <h3>To create</h3>
      <ul>
        @for (label of state.creationLabels(); track label) {
          <li>{{ label }}</li>
        }
      </ul>
      <div class="actions">
        <button mat-flat-button type="button" [disabled]="busy()" (click)="createAll()">
          <mat-icon>add</mat-icon>
          Create
        </button>
      </div>
    } @else if (state.savedPath(); as path) {
      <section class="outcome" role="status">
        <h3>
          Saved to <span class="mono">{{ path }}</span>
        </h3>
        <p>Apply it to the controller with</p>
        <p class="command">
          <span class="mono">{{ applyCommand(path) }}</span>
          <flock-copy-button [text]="applyCommand(path)" />
        </p>
      </section>
    } @else if (state.fileText(); as text) {
      <pre class="file">{{ text }}</pre>

      @if (state.mode() === 'cli') {
        <div class="save">
          <mat-form-field class="path">
            <mat-label>Save to</mat-label>
            <input
              matInput
              name="savePath"
              autocomplete="off"
              [ngModel]="state.savePath()"
              (ngModelChange)="state.savePath.set($event)"
            />
            <mat-hint>Relative to {{ state.cwd() }}</mat-hint>
          </mat-form-field>
          <button
            mat-flat-button
            type="button"
            [disabled]="busy() || state.savePath().trim() === ''"
            (click)="save(text)"
          >
            <mat-icon>save</mat-icon>
            Save
          </button>
        </div>
      } @else {
        <div class="actions">
          <button mat-stroked-button type="button" (click)="copy(text)">
            <mat-icon>content_copy</mat-icon>
            Copy
          </button>
          <button mat-stroked-button type="button" (click)="download(text)">
            <mat-icon>download</mat-icon>
            Download
          </button>
        </div>
        <p class="note">
          Keep the file before you close this panel, the next opening starts a new run.
        </p>
      }

      @if (problems().length > 0) {
        <div role="alert">
          <p class="problem">
            <mat-icon>error</mat-icon>
            The cli refused the file:
          </p>
          <ul class="mono">
            @for (problem of problems(); track $index) {
              <li>{{ problemLine(problem) }}</li>
            }
          </ul>
        </div>
      }
    } @else if (state.buildError()) {
      <p class="problem" role="alert">
        <mat-icon>error</mat-icon>
        The documents can't be written: {{ state.buildError() }}
      </p>
    }
    @if (error()) {
      <p class="problem" role="alert">
        <mat-icon>error</mat-icon>
        {{ error() }}
      </p>
    }

    <div class="actions">
      @if (!isFinished()) {
        <button mat-button type="button" (click)="state.step.set(steps.backend)">Back</button>
      }
      <span class="spacer"></span>
      <button mat-button type="button" (click)="state.reset()">Start over</button>
    </div>
  `,
  styleUrl: './step.scss',
  styles: `
    pre {
      overflow-x: auto;
      margin: 0;
      padding: 8px 12px;
      border: 1px solid var(--flock-border);
      border-radius: 8px;
      font-family: var(--flock-mono);
    }
    .file {
      max-height: 50vh;
    }
    .outcome {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .command {
      display: flex;
      align-items: center;
      gap: 4px;
    }
    .save {
      display: flex;
      flex-wrap: wrap;
      align-items: baseline;
      gap: 8px;
    }
    .path {
      flex: 1 1 320px;
    }
    ul {
      margin: 4px 0 0;
    }
  `,
})
export class ReviewStep {
  protected state = inject(BuilderState);
  private cli = inject(CliHost);
  private dialog = inject(MatDialog);
  private clipboard = inject(Clipboard);
  private notifier = inject(Notifier);

  protected readonly steps = steps;
  protected busy = signal(false);
  protected error = signal('');
  protected problems = signal<FileProblem[]>([]);
  // a run that created resources or saved its file is over, only Start over begins another one
  protected isFinished = computed(
    () => this.state.outcome() !== null || this.state.savedPath() !== '',
  );

  constructor() {
    // the problems the cli found belong to the text it was sent, an edit of the run makes them stale
    effect(() => {
      this.state.fileText();
      this.problems.set([]);
    });
  }

  protected async createAll() {
    this.busy.set(true);
    try {
      await this.state.createAll();
    } finally {
      this.busy.set(false);
    }
  }

  protected applyCommand(path: string): string {
    // a path with spaces or shell characters stays one argument
    const isPlain = /^[\w./:@-]+$/.test(path);
    const quoted = isPlain ? path : `'${path.replaceAll("'", "'\\''")}'`;
    return `flock apply -f ${quoted}`;
  }

  protected problemLine(problem: FileProblem): string {
    const where = problem.index > 0 ? `document ${problem.index}` : problem.file;
    const field = problem.field ? ` ${problem.field}` : '';
    return `${where}${field}: ${problem.message}`;
  }

  // save writes a new file, and replaces one that exists only once the user agreed
  protected async save(text: string) {
    const path = this.state.savePath().trim();
    this.busy.set(true);
    this.error.set('');
    this.problems.set([]);
    try {
      let answer = await this.cli.writeFile(path, text, false);
      if (!answer.ok && 'exists' in answer) {
        const agreed = await confirm(this.dialog, {
          title: 'Overwrite the file?',
          message: `${path} already exists, saving replaces its content.`,
          confirm: 'Overwrite',
        });
        if (!agreed) {
          return;
        }
        answer = await this.cli.writeFile(path, text, true);
      }
      if (!answer.ok) {
        this.problems.set('problems' in answer ? answer.problems : []);
        return;
      }
      this.state.saved(path);
    } catch (err) {
      this.error.set(err instanceof Error ? err.message : String(err));
    } finally {
      this.busy.set(false);
    }
  }

  protected copy(text: string) {
    if (this.clipboard.copy(text)) {
      this.notifier.info('Copied the file');
    }
  }

  protected download(text: string) {
    downloadFile(newFileName, text);
  }
}
