import { Component, computed, input, signal } from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { MtxPopoverModule } from '@ng-matero/extensions/popover';
import { Backend } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { HealthCheck } from '../../gen/flock/api/config/core/v1/healthcheck_pb';
import {
  backendType,
  codecLabels,
  durationLabel,
  formatUrl,
  healthCheckType,
  httpMethodLabel,
  lbPolicyLabel,
  lookupFamilyLabels,
  payloadsToJson,
  proxyProtocolLabel,
} from '../core/display';
import { JsonTree } from './json-tree';

// BackendEndpoints is the endpoint configuration of a backend: dns host, static ips or eds url
@Component({
  selector: 'flock-backend-endpoints',
  template: `
    @if (dns(); as dns) {
      <div class="config">
        <i>Host</i><span>{{ dns.host }}</span> <i>Port</i><span>{{ dns.port }}</span> <i>Lookup</i
        ><span>{{ lookupFamilyLabels[dns.lookupFamily] }}</span>
      </div>
    }
    @if (ipAddrGroup(); as group) {
      @for (addr of group.ipAddresses; track $index) {
        <div>
          {{ addr.ip }}:{{ addr.port }}
          @if (addr.loadBalancingWeight > 0) {
            (weight: {{ addr.loadBalancingWeight }})
          }
        </div>
      }
    }
    @if (eds(); as eds) {
      <div class="config">
        <strong>Url</strong><span>{{ formatUrl(eds.url) }}</span>
        @if (eds.serviceName) {
          <strong>Service Name</strong><span>{{ eds.serviceName }}</span>
        }
      </div>
    }
  `,
  styles: `
    // a backend may list many addresses: they scroll instead of stretching every card of the row
    :host {
      display: block;
      max-height: 160px;
      overflow-y: auto;
    }
    .config {
      display: grid;
      grid-template-columns: auto 1fr;
      gap: 4px 10px;
    }
  `,
})
export class BackendEndpoints {
  readonly backend = input.required<Backend>();
  protected dns = computed(() => {
    const endpoint = this.backend().endpoint;
    return endpoint.case === 'dns' ? endpoint.value : null;
  });
  protected ipAddrGroup = computed(() => {
    const endpoint = this.backend().endpoint;
    return endpoint.case === 'ipAddrGroup' ? endpoint.value : null;
  });
  protected eds = computed(() => {
    const endpoint = this.backend().endpoint;
    return endpoint.case === 'eds' ? endpoint.value : null;
  });
  protected readonly lookupFamilyLabels = lookupFamilyLabels;
  protected readonly formatUrl = formatUrl;
}

// BackendConfig is the card grid of a backend's configuration
@Component({
  selector: 'flock-backend-config',
  imports: [MatCardModule, BackendEndpoints],
  template: `
    <div class="cards">
      <mat-card appearance="outlined">
        <mat-card-header>
          <mat-card-subtitle>Type</mat-card-subtitle>
          <mat-card-title>{{ type() }}</mat-card-title>
        </mat-card-header>
      </mat-card>
      <mat-card appearance="outlined">
        <mat-card-header>
          <mat-card-subtitle>LB Policy</mat-card-subtitle>
          <mat-card-title>{{ lbPolicy() }}</mat-card-title>
        </mat-card-header>
      </mat-card>
      <mat-card appearance="outlined">
        <mat-card-header>
          <mat-card-subtitle>HealthCheck Type</mat-card-subtitle>
          <mat-card-title>{{ hcType() }}</mat-card-title>
        </mat-card-header>
      </mat-card>
      <mat-card appearance="outlined">
        <mat-card-header>
          <mat-card-subtitle>Proxy Protocol</mat-card-subtitle>
          <mat-card-title [class]="backend().enableProxyProtocol ? 'good' : 'bad'">{{
            proxyProtocol()
          }}</mat-card-title>
        </mat-card-header>
      </mat-card>
      <mat-card appearance="outlined">
        <mat-card-header>
          <mat-card-subtitle>Endpoint Configuration</mat-card-subtitle>
        </mat-card-header>
        <mat-card-content><flock-backend-endpoints [backend]="backend()" /></mat-card-content>
      </mat-card>
    </div>
  `,
  styles: `
    .good {
      color: var(--flock-ok-text);
    }
    .bad {
      color: var(--flock-error-text);
    }
  `,
})
export class BackendConfig {
  readonly backend = input.required<Backend>();
  protected type = computed(() => backendType(this.backend()));
  protected lbPolicy = computed(() => lbPolicyLabel(this.backend()));
  protected hcType = computed(() => healthCheckType(this.backend().healthCheck));
  protected proxyProtocol = computed(() => proxyProtocolLabel(this.backend()));
}

interface Card {
  label: string;
  value?: string;
  // shown as json in a popover, payloads and plugin options are too long for a card
  json?: unknown;
  jsonLabel?: string;
}

// HealthCheckConfig is the card grid of a health check, the type specific cards after the common ones
@Component({
  selector: 'flock-healthcheck-config',
  imports: [MatCardModule, MtxPopoverModule, JsonTree],
  template: `
    <mtx-popover #popoverJson="mtxPopover" [closeOnPanelClick]="true">
      <flock-json-tree [value]="popoverValue()" />
    </mtx-popover>
    @if (healthCheck(); as hc) {
      <div class="cards">
        @for (card of cards(); track card.label) {
          <mat-card appearance="outlined">
            <mat-card-header>
              <mat-card-subtitle>{{ card.label }}</mat-card-subtitle>
              <mat-card-title>
                @if (card.json !== undefined) {
                  <span
                    class="chip clickable unknown"
                    [mtxPopoverTriggerFor]="popoverJson"
                    mtxPopoverTriggerOn="hover"
                    (mouseenter)="popoverValue.set(card.json)"
                    >{{ card.jsonLabel }}</span
                  >
                } @else {
                  {{ card.value }}
                }
              </mat-card-title>
            </mat-card-header>
          </mat-card>
        }
      </div>
    } @else {
      <p>No health check set.</p>
    }
  `,
})
export class HealthCheckConfig {
  readonly healthCheck = input.required<HealthCheck | undefined>();
  protected popoverValue = signal<unknown>(null);

  protected cards = computed<Card[]>(() => {
    const hc = this.healthCheck();
    if (!hc) {
      return [];
    }
    const common: Card[] = [
      { label: 'Timeout', value: durationLabel(hc.timeout) },
      { label: 'Interval', value: durationLabel(hc.interval) },
      { label: 'Healthy Threshold', value: String(hc.healthyThreshold) },
      { label: 'Unhealthy Interval', value: durationLabel(hc.unhealthyInterval) },
      { label: 'Unhealthy Threshold', value: String(hc.unhealthyThreshold) },
      { label: 'Tls Enable', value: hc.enableTls ? 'Enabled' : 'Disabled' },
      { label: 'Type', value: healthCheckType(hc) },
    ];
    return [...common, ...this.typeCards(hc)];
  });

  private typeCards(hc: HealthCheck): Card[] {
    const checker = hc.healthChecker;
    const cards: Card[] = [];
    switch (checker.case) {
      case 'httpHealthCheck': {
        const http = checker.value;
        if (http.host) {
          cards.push({ label: 'Http Host', value: http.host });
        }
        if (http.path) {
          cards.push({ label: 'Http Path', value: http.path });
        }
        cards.push({ label: 'Http Method', value: httpMethodLabel(http.method) });
        if (http.expectedStatuses) {
          // the range end is exclusive
          const start = http.expectedStatuses.start;
          const last = http.expectedStatuses.end - 1n;
          cards.push({
            label: 'Http Expected Status',
            value: start === last ? `${start}` : `${start} - ${last}`,
          });
        }
        cards.push({ label: 'Http Codec', value: codecLabels[http.codecClientType] });
        if (http.send) {
          cards.push({
            label: 'Http Send',
            json: payloadsToJson([http.send]),
            jsonLabel: 'Show send',
          });
        }
        if (http.receive) {
          cards.push({
            label: 'Http Receive',
            json: payloadsToJson([http.receive]),
            jsonLabel: 'Show receive',
          });
        }
        break;
      }
      case 'tcpHealthCheck':
      case 'udpHealthCheck': {
        const prefix = checker.case === 'tcpHealthCheck' ? 'Tcp' : 'Udp';
        if (checker.value.send) {
          cards.push({
            label: `${prefix} Send`,
            json: payloadsToJson([checker.value.send]),
            jsonLabel: 'Show send',
          });
        }
        if (checker.value.receive.length > 0) {
          cards.push({
            label: `${prefix} Receive`,
            json: payloadsToJson(checker.value.receive),
            jsonLabel: 'Show receive',
          });
        }
        break;
      }
      case 'grpcHealthCheck':
        if (checker.value.serviceName) {
          cards.push({ label: 'GRPC Service Name', value: checker.value.serviceName });
        }
        if (checker.value.authority) {
          cards.push({ label: 'GRPC Authority', value: checker.value.authority });
        }
        break;
      case 'pluginHealthCheck':
        cards.push({ label: 'Plugin Name', value: checker.value.name });
        cards.push({
          label: 'Plugin Options',
          json: checker.value.options ?? {},
          jsonLabel: 'Show options',
        });
        break;
    }
    return cards;
  }
}
