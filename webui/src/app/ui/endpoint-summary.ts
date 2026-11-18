import { Component, computed, inject, input } from '@angular/core';
import { MtxPopoverModule } from '@ng-matero/extensions/popover';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { healthLevel, vsBackendUuids } from '../core/display';
import { InstanceHealth } from '../core/instance-health';

// EndpointSummary is the "h/t endpoints are healthy" chip of a virtual server, its popover lists each endpoint
@Component({
  selector: 'flock-endpoint-summary',
  imports: [MtxPopoverModule],
  template: `
    <mtx-popover #popover="mtxPopover" [closeOnPanelClick]="true">
      @for (stat of stats(); track stat.backendUuid) {
        <div class="stat-row">
          @for (endpoint of stat.endpointStats; track $index) {
            <p>
              <strong>{{ endpoint.ipAddr?.ip }}:{{ endpoint.ipAddr?.port }}</strong> is
              <span [class]="endpoint.healthy ? 'good' : 'bad'">{{
                endpoint.healthy ? 'Healthy' : 'Unhealthy'
              }}</span>
              @if (endpoint.lastError) {
                <span class="last-error">{{ endpoint.lastError }}</span>
              }
            </p>
          }
        </div>
      }
    </mtx-popover>

    @if (backendUuids().length === 0) {
      <span class="chip ok">No backend set</span>
    } @else if (health(); as health) {
      @if (health.total === 0) {
        <span class="chip unknown">No endpoint reported</span>
      } @else {
        <span
          [class]="'chip clickable ' + level()"
          [mtxPopoverTriggerFor]="popover"
          mtxPopoverTriggerOn="hover"
        >
          {{ health.healthy }}/{{ health.total }} endpoints are healthy
        </span>
      }
    } @else {
      <span class="chip unknown">Loading state ...</span>
    }
  `,
  styles: `
    p {
      margin: 4px 0;
    }
    .stat-row + .stat-row {
      border-top: 1px solid var(--flock-border);
    }
    .good {
      color: var(--flock-ok-text);
      font-weight: 600;
    }
    .bad {
      color: var(--flock-error-text);
      font-weight: 600;
    }
    .last-error {
      display: block;
      font-family: var(--flock-mono);
      font-size: 12px;
      color: var(--flock-error-text);
    }
  `,
})
export class EndpointSummary {
  readonly vs = input.required<VirtualServer>();

  private instanceHealth = inject(InstanceHealth);
  protected backendUuids = computed(() => vsBackendUuids(this.vs()));
  protected health = computed(() => this.instanceHealth.health(this.vs()));
  protected stats = computed(() => {
    const byBackend = this.instanceHealth.endpointStats();
    return this.backendUuids().flatMap((uuid) => byBackend?.get(uuid) ?? []);
  });
  protected level = computed(() => {
    const health = this.health();
    return health ? healthLevel(health.healthy, health.total) : 'unknown';
  });
}
