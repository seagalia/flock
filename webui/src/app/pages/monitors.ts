import {
  afterNextRender,
  Component,
  DestroyRef,
  effect,
  ElementRef,
  inject,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDividerModule } from '@angular/material/divider';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Code, ConnectError } from '@connectrpc/connect';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import { ActivatedRoute } from '@angular/router';
import { Agent } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { Api } from '../core/api';
import { Instances } from '../core/instances';

const allAgents = '';
// ansi foreground colors given in turn to the agents, so that the lines of one agent look alike
const agentColors = [31, 32, 33, 34, 35, 36, 30];
const noticeStyle = '\x1b[37;48;5;239;3m';
const reset = '\x1b[0m';

// Monitors streams the logs of the agents of an instance in a terminal
@Component({
  selector: 'flock-monitors',
  imports: [
    MatFormFieldModule,
    MatSelectModule,
    MatButtonModule,
    MatIconModule,
    MatTooltipModule,
    MatDividerModule,
    MatProgressSpinnerModule,
  ],
  template: `
    <h1>Monitors</h1>
    <mat-divider />
    @if (instances.current(); as instance) {
      <div class="control">
        <mat-form-field subscriptSizing="dynamic">
          <mat-label>Select an Instance</mat-label>
          <mat-select [value]="instance" (selectionChange)="instances.choose($event.value)">
            @for (one of instances.all(); track one.UUID) {
              <mat-option [value]="one">{{ one.name }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        <mat-form-field subscriptSizing="dynamic">
          <mat-label>Select an Agent</mat-label>
          <mat-select [value]="agentUuid()" (selectionChange)="agentUuid.set($event.value)">
            <mat-option [value]="allAgents">All</mat-option>
            @for (agent of instances.agents(); track agent.UUID) {
              <mat-option [value]="agent.UUID">{{ agentLabel(agent) }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
        <button mat-flat-button (click)="terminal?.clear()">
          <mat-icon>cancel_presentation</mat-icon>
          Clear
        </button>
        <button mat-flat-button (click)="paused.set(!paused())">
          <mat-icon>{{ paused() ? 'play_circle' : 'pause_circle' }}</mat-icon>
          {{ paused() ? 'Resume' : 'Pause' }}
        </button>
        <a
          mat-stroked-button
          class="metrics"
          [href]="'/metrics/' + instance.UUID"
          target="_blank"
          matTooltip="In Openmetrics format for using with Prometheus"
        >
          <mat-icon>launch</mat-icon>
          See Instance Metrics
        </a>
      </div>
    }
    <div class="term-box">
      <div class="term" #term></div>
      @if (waiting()) {
        <div class="waiting" role="status">
          <mat-spinner diameter="36" />
          <span>Waiting for the logs of the agents…</span>
        </div>
      }
    </div>
  `,
  styles: `
    .control {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 12px 20px;
      margin: 16px 0;
    }
    .metrics {
      color: var(--mat-sys-tertiary);
    }
    .term-box {
      position: relative;
    }
    .waiting {
      position: absolute;
      inset: 0;
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      gap: 16px;
      color: #c9d6e3;
      pointer-events: none;
      --mat-progress-spinner-active-indicator-color: #c9d6e3;
    }
    .term {
      height: 70vh;
      padding: 8px;
      border-radius: 12px;
      background: #0b1f36;
      overflow: hidden;
    }
  `,
})
export class Monitors {
  private api = inject(Api);
  protected instances = inject(Instances);
  // ?agent= comes from the agent panel, which opens this page on one agent
  protected agentUuid = signal(
    inject(ActivatedRoute).snapshot.queryParamMap.get('agent') ?? allAgents,
  );
  protected paused = signal(false);
  // true from the start of a stream until its first log line or its end
  protected waiting = signal(false);
  protected readonly allAgents = allAgents;
  protected terminal: Terminal | null = null;
  private termElement = viewChild.required<ElementRef<HTMLElement>>('term');
  private stream: AbortController | null = null;
  private colors = new Map<string, number>();
  private resize: ResizeObserver | null = null;

  constructor() {
    this.instances.load(inject(ActivatedRoute).snapshot.queryParamMap.get('instance'));
    afterNextRender(() => this.openTerminal());
    effect(() => {
      const instance = this.instances.current();
      const agentUuid = this.agentUuid();
      if (instance) {
        untracked(() => this.startStream(instance.UUID, agentUuid));
      }
    });
    inject(DestroyRef).onDestroy(() => {
      this.stream?.abort();
      this.resize?.disconnect();
      this.terminal?.dispose();
    });
  }

  protected agentLabel(agent: Agent): string {
    return agent.name ? `${agent.name} (${agent.UUID})` : agent.UUID;
  }

  private openTerminal() {
    const fit = new FitAddon();
    this.terminal = new Terminal({
      cursorStyle: 'bar',
      cursorInactiveStyle: 'none',
      disableStdin: true,
      convertEol: true,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 13,
      theme: { background: '#0b1f36' },
    });
    this.terminal.loadAddon(fit);
    this.terminal.open(this.termElement().nativeElement);
    fit.fit();
    this.resize = new ResizeObserver(() => fit.fit());
    this.resize.observe(this.termElement().nativeElement);
  }

  // startStream replaces the stream of the previous selection, the server stops sending when it is aborted
  private async startStream(instanceUuid: string, agentUuid: string) {
    this.stream?.abort();
    const stream = new AbortController();
    this.stream = stream;
    this.terminal?.clear();
    this.notice('Starting log stream...');
    this.waiting.set(true);
    try {
      const lines = this.api.lookup.getLogsStream(
        { instanceUuid, agentUuid },
        { signal: stream.signal },
      );
      for await (const resp of lines) {
        // the stream starts with a started message, the loader stays until a line
        if (resp.event.case !== 'logStream') {
          continue;
        }
        this.waiting.set(false);
        // a paused stream stays open, its lines are dropped
        if (this.paused()) {
          continue;
        }
        const line = resp.event.value;
        this.terminal?.writeln(
          `${noticeStyle}Agent \x1b[${this.colorOf(line.agentUuid)};48;5;239;3m${line.agentUuid}${reset}   ${line.logLine.trimEnd()}`,
        );
      }
      this.waiting.set(false);
      this.notice('Log stream ended');
    } catch (err) {
      // an aborted stream was replaced by the next one, which owns the loader now
      if (stream.signal.aborted) {
        return;
      }
      this.waiting.set(false);
      const connectErr = ConnectError.from(err);
      const reason =
        connectErr.code === Code.Unknown
          ? connectErr.rawMessage
          : `${Code[connectErr.code]}: ${connectErr.rawMessage}`;
      this.notice(`Log stream ended: ${reason}`);
    }
  }

  private colorOf(agentUuid: string): number {
    if (!this.colors.has(agentUuid)) {
      this.colors.set(agentUuid, agentColors[this.colors.size % agentColors.length]);
    }
    return this.colors.get(agentUuid)!;
  }

  private notice(text: string) {
    this.terminal?.writeln(`${noticeStyle}${text}${reset}`);
  }
}
