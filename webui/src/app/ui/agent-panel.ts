import { DatePipe } from '@angular/common';
import { Component, computed, input, output } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { RouterLink } from '@angular/router';
import { Instance } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { AgentView, extensionGroups, levelLabels, levelOf, timeAgo } from '../core/agents';
import { kinds } from '../core/resources';
import { SidePanel } from './side-panel';

// AgentPanel details one agent of an instance: its state and components, the VIPs it announces, the extensions its
// node reports and where to read its logs, status and metrics
@Component({
  selector: 'flock-agent-panel',
  imports: [SidePanel, RouterLink, DatePipe, MatButtonModule, MatIconModule],
  template: `
    <flock-side-panel
      #panel
      [title]="view().isProvisioner ? 'Provisioner' : 'Agent'"
      [subject]="view().agent.name"
      (closed)="closed.emit()"
    >
      <span panelHeader [class]="'chip level ' + view().level">{{ levelLabel() }}</span>
      <dl class="facts">
        <dt>Instance</dt>
        <dd>{{ instance().name }}</dd>
        <dt>UUID</dt>
        <dd class="mono">{{ view().agent.UUID }}</dd>
        <dt>Host</dt>
        <dd>{{ view().agent.host }}</dd>
        @if (!view().isProvisioner) {
          <dt>Mode</dt>
          <dd>{{ view().isBridge ? 'bridge' : 'sidecar' }}</dd>
        }
        <dt>Version</dt>
        <dd>{{ view().agent.type }} {{ view().version || 'unknown' }}</dd>
        <dt>Last connected</dt>
        <dd>
          @if (view().lastSeen; as lastSeen) {
            {{ lastSeen | date: 'yyyy-MM-dd HH:mm' }} ({{ ago(lastSeen) }})
          } @else {
            Never seen by a controller
          }
        </dd>
      </dl>

      <section>
        <h3>{{ view().isBridge ? 'Controlled components' : 'Components' }}</h3>
        @if (ownError(); as ownError) {
          <div class="line">
            <span class="state-dot error"></span><span class="what">agent</span>
            <span class="details">{{ ownError }}</span>
          </div>
        }
        @for (component of view().components; track component.name) {
          <div class="line">
            <span [class]="'state-dot ' + componentLevel(component.status)"></span>
            <span class="what">{{ component.name }}</span>
            <span class="details">{{ component.details }}</span>
          </div>
        } @empty {
          @if (view().level === 'unknown') {
            <p class="muted">This agent gives no status.</p>
          }
        }
      </section>

      @if (!view().isProvisioner) {
        <section>
          <h3>VIPs announced</h3>
          @if (view().isBridge) {
            @for (component of view().bridgeComponents; track component.name) {
              <div class="line">
                <span [class]="'state-dot ' + component.level"></span>
                <span class="what">{{ component.name }}</span>
                <span class="vips">
                  @for (vip of component.vips; track vip.UUID) {
                    <a [routerLink]="vipLink" [queryParams]="{ uuid: vip.UUID }">{{ vip.name }}</a>
                  } @empty {
                    <span class="muted">none</span>
                  }
                </span>
              </div>
            }
          } @else if (view().vips; as vips) {
            <div class="vips">
              @for (vip of vips; track vip.UUID) {
                <a [routerLink]="vipLink" [queryParams]="{ uuid: vip.UUID }">{{ vip.name }}</a>
              } @empty {
                <span class="muted"
                  >None, the VIPs of the instance are announced by other agents.</span
                >
              }
            </div>
          } @else {
            <p class="muted">Unknown while the agent gives no status.</p>
          }
        </section>
      }

      <section>
        <h3>Extensions</h3>
        @for (group of extensions(); track group.category) {
          <div class="group">
            <span class="category">{{ group.category }}</span>
            <span class="names">
              @for (extension of group.extensions; track extension.typeUrl) {
                <span
                  [class.disabled]="extension.disabled"
                  [title]="
                    extension.typeUrl + (extension.disabled ? ' (disabled on this agent)' : '')
                  "
                  >{{ extension.name }}</span
                >
              }
            </span>
          </div>
        } @empty {
          <p class="muted">No extension reported, the agent has not connected yet.</p>
        }
      </section>

      <section>
        <h3>Features</h3>
        <p>
          {{ features().on.join(' · ') || 'none' }}
          @if (features().off.length > 0) {
            <span class="muted">(no {{ features().off.join(', no ') }})</span>
          }
        </p>
      </section>

      <div panelActions class="actions">
        @if (view().agent.features?.showLogs) {
          <a
            mat-stroked-button
            routerLink="/monitors"
            [queryParams]="{ instance: instance().name, agent: view().agent.UUID }"
          >
            <mat-icon>terminal</mat-icon>
            View logs
          </a>
        }
        @if (view().agent.features?.showStatus) {
          <a
            mat-stroked-button
            routerLink="/status"
            [queryParams]="{ instance: instance().name, agent: view().agent.UUID }"
          >
            <mat-icon>fact_check</mat-icon>
            Resource status
          </a>
        }
        @if (view().agent.features?.showMetrics) {
          <a mat-stroked-button [href]="'/metrics/' + instance().UUID" target="_blank">
            <mat-icon>launch</mat-icon>
            Metrics
          </a>
        }
        <button mat-flat-button (click)="panel.close()">
          <mat-icon>done</mat-icon>
          Ok
        </button>
      </div>
    </flock-side-panel>
  `,
  styles: `
    // the chip sits in the panel title, it keeps the size of the other chips
    .level {
      font-size: 13px;
      min-height: 24px;
    }
    .facts {
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: 4px 16px;
      margin: 0 0 8px;
      dt {
        color: var(--mat-sys-on-surface-variant);
      }
      dd {
        margin: 0;
        overflow-wrap: anywhere;
      }
    }
    .mono {
      font-family: var(--flock-mono);
      font-size: 13px;
    }
    section {
      margin-top: 18px;
    }
    h3 {
      margin: 0 0 6px;
      font-size: 13px;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.06em;
      color: var(--mat-sys-on-surface-variant);
    }
    .line {
      display: flex;
      align-items: baseline;
      gap: 8px;
      padding: 2px 0;
      .state-dot {
        align-self: center;
      }
    }
    .what {
      min-width: 80px;
      font-weight: 600;
    }
    .details {
      color: var(--mat-sys-on-surface-variant);
    }
    .vips {
      display: flex;
      flex-wrap: wrap;
      gap: 4px 12px;
    }
    .group {
      display: grid;
      gap: 2px;
      margin-bottom: 8px;
    }
    .category {
      font-family: var(--flock-mono);
      font-size: 12px;
      color: var(--mat-sys-on-surface-variant);
    }
    .names {
      display: flex;
      flex-wrap: wrap;
      gap: 2px 12px;
      span {
        cursor: help;
      }
      .disabled {
        color: var(--mat-sys-on-surface-variant);
        text-decoration: line-through;
      }
    }
    .muted {
      color: var(--mat-sys-on-surface-variant);
    }
    p {
      margin: 0;
    }
    .actions {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
    }
  `,
})
export class AgentPanel {
  readonly view = input.required<AgentView>();
  readonly instance = input.required<Instance>();
  readonly closed = output<void>();

  protected readonly vipLink = kinds.vip.link;
  protected readonly componentLevel = levelOf;
  protected readonly ago = (date: Date) => timeAgo(date);
  protected levelLabel = computed(() => levelLabels[this.view().level]);
  // an agent failing before reporting any component (unreachable, ...) gets its error as the only line
  protected ownError = computed(() => {
    const view = this.view();
    const failedAlone = view.level === 'error' && view.components.length === 0;
    return failedAlone ? view.cause : '';
  });
  protected extensions = computed(() => extensionGroups(this.view().node?.node?.extensions ?? []));
  protected features = computed(() => {
    const features = this.view().agent.features;
    const all: [string, boolean][] = [
      ['logs', !!features?.showLogs],
      ['metrics', !!features?.showMetrics],
      ['status', !!features?.showStatus],
      ['L3 logs', !!features?.showL3Log],
    ];
    return {
      on: all.filter(([, enabled]) => enabled).map(([label]) => label),
      off: all.filter(([, enabled]) => !enabled).map(([label]) => label),
    };
  });
}
