import { Component, computed, input, output } from '@angular/core';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterLink } from '@angular/router';
import { Instance } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { timestampDate } from '@bufbuild/protobuf/wkt';
import { InstanceNode } from '../../gen/flock/services/lookup/v1/lookup_pb';
import { AgentsReport, AgentView, isVersionDrift, timeAgo } from '../core/agents';
import { kinds } from '../core/resources';

// AgentBand shows the agents of the Overview's instance as tiles: state, version, VIPs announced and why one is not
// ok. A tile click asks the page to open the agent panel.
@Component({
  selector: 'flock-agent-band',
  imports: [RouterLink, MatTooltipModule],
  template: `
    <section class="band" aria-label="Agents">
      <div class="head">
        <h2>Agents</h2>
        @if (loading()) {
          <span class="meta">Loading state ...</span>
        } @else if (report().agents.length > 0) {
          <span class="meta">
            {{ healthy() }} / {{ report().agents.length }} healthy ·
            <a [routerLink]="instanceLink" [queryParams]="{ uuid: instance().UUID }"
              >all details on the Instances page</a
            >
          </span>
        }
      </div>
      <div class="tiles">
        @for (view of report().agents; track view.agent.UUID) {
          <button
            type="button"
            [class]="'tile ' + view.level"
            [class.active]="view.agent.UUID === activeUuid()"
            (click)="picked.emit(view)"
          >
            <span class="top">
              <span [class]="'state-dot ' + view.level"></span>
              <span class="name">{{ view.agent.name }}</span>
            </span>
            <span class="meta">
              {{ view.isBridge ? 'bridge' : 'sidecar' }}
              @if (view.version) {
                · {{ view.version }}
                @if (isDrift(view)) {
                  <span class="drift" [matTooltip]="'Differs from ' + report().commonVersion"
                    >⚠</span
                  >
                }
              }
              · {{ vipCount(view) }}
            </span>
            @if (view.cause) {
              <span [class]="'cause ' + view.level">{{ view.cause }}</span>
            }
          </button>
        } @empty {
          @if (!loading()) {
            <span class="tile ghost">
              <span class="meta">
                No agent yet. Agents join this instance from the instantiator config of its zone.
              </span>
            </span>
          }
        }
        @for (node of report().undeclared; track node.name) {
          <span class="tile ghost" [matTooltip]="undeclaredTooltip(node)">
            <span class="top">
              <span class="state-dot"></span>
              <span class="name">{{ node.name }}</span>
            </span>
            <span class="meta">connected, not declared</span>
          </span>
        }
      </div>
    </section>
  `,
  styles: `
    .band {
      display: grid;
      gap: 8px;
      padding: 10px 16px 14px;
      border-bottom: 1px solid var(--flock-border);
    }
    .head {
      display: flex;
      flex-wrap: wrap;
      align-items: baseline;
      justify-content: space-between;
      gap: 8px;
      h2 {
        margin: 0;
        font-size: 16px;
      }
    }
    .meta {
      color: var(--mat-sys-on-surface-variant);
      font-size: 13px;
    }
    .tiles {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
    }
    .tile {
      display: grid;
      gap: 2px;
      flex: 0 1 260px;
      min-width: 200px;
      padding: 8px 12px;
      text-align: left;
      font: inherit;
      color: inherit;
      background: var(--flock-bg);
      border: 1px solid var(--flock-border);
      border-left: 4px solid var(--flock-unknown);
      border-radius: 8px;
      cursor: pointer;
      &:hover {
        background: var(--flock-hover);
      }
      &.active {
        background: var(--flock-selected);
      }
      &.ok {
        border-left-color: var(--flock-ok);
      }
      &.warning {
        border-left-color: var(--flock-warning);
      }
      &.error {
        border-left-color: var(--flock-error);
      }
      &.ghost {
        border-style: dashed;
        border-left-width: 1px;
        cursor: default;
        &:hover {
          background: var(--flock-bg);
        }
      }
    }
    .top {
      display: flex;
      align-items: center;
      gap: 8px;
    }
    .name {
      font-weight: 600;
    }
    .drift {
      color: var(--flock-warning-text);
      font-weight: 600;
    }
    .cause {
      font-size: 13px;
      &.warning {
        color: var(--flock-warning-text);
      }
      &.error {
        color: var(--flock-error-text);
      }
    }
  `,
})
export class AgentBand {
  readonly instance = input.required<Instance>();
  readonly report = input.required<AgentsReport>();
  // true until the first status of the instance is read
  readonly loading = input(false);
  readonly activeUuid = input<string | null>(null);
  readonly picked = output<AgentView>();

  protected readonly instanceLink = kinds.instance.link;
  protected healthy = computed(
    () => this.report().agents.filter((view) => view.level === 'ok').length,
  );

  protected isDrift(view: AgentView): boolean {
    return isVersionDrift(this.report(), view);
  }

  protected vipCount(view: AgentView): string {
    if (view.isBridge) {
      const perComponent = view.bridgeComponents.map(
        (component) => `${component.name} ${component.vips.length}`,
      );
      return perComponent.length > 0 ? `${perComponent.join(' · ')} VIPs` : 'VIPs unknown';
    }
    if (!view.vips) {
      return 'VIPs unknown';
    }
    return view.vips.length === 1 ? '1 VIP' : `${view.vips.length} VIPs`;
  }

  protected undeclaredTooltip(node: InstanceNode): string {
    const seen = node.lastSeen ? `Node seen ${timeAgo(timestampDate(node.lastSeen))}. ` : '';
    return `${seen}No agent of the instantiator config is paired with it.`;
  }
}
