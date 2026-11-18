import { Component, computed, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { Choice, ChoiceCards } from '../ui/choice-cards';
import { BuilderState, steps } from './builder-state';
import { BackendInput, Discovery, edsUrl } from './documents';
import { SecretRefPicker } from '../forms/secret-ref-picker';
import { ProposedName } from '../ui/proposed-name';

type Endpoint = BackendInput['endpoints'][number];
type HealthCheckType = BackendInput['healthCheck']['type'];

const discoveryCards: Choice[] = [
  { value: 'list', title: 'Endpoint list', line: 'Fixed addresses and ports' },
  { value: 'dns', title: 'DNS', line: 'Each address a name resolves to' },
  { value: 'eds', title: 'Discovery server', line: 'The endpoints a server such as vigie gives' },
];

// BackendStep gives the virtual server its backend, a new one or one of the controller by name
@Component({
  selector: 'flock-backend-step',
  imports: [
    ProposedName,
    ChoiceCards,
    FormsModule,
    MatButtonModule,
    MatButtonToggleModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatSlideToggleModule,
    SecretRefPicker,
  ],
  template: `
    <mat-button-toggle-group
      name="backendMode"
      aria-label="Backend"
      [value]="state.backendMode()"
      (change)="state.backendMode.set($event.value)"
    >
      <mat-button-toggle value="create">New backend</mat-button-toggle>
      <mat-button-toggle value="existing" [disabled]="!hasExisting()">
        Existing backend
      </mat-button-toggle>
    </mat-button-toggle-group>

    @if (state.backendMode() === 'existing') {
      <mat-form-field>
        <mat-label>Backend</mat-label>
        <mat-select
          name="existingBackend"
          [value]="state.existingBackendUuid()"
          (selectionChange)="state.existingBackendUuid.set($event.value)"
        >
          @for (backend of state.controllerBackends(); track backend.UUID) {
            <mat-option [value]="backend.UUID">
              {{ backend.name }} <span class="uuid">{{ backend.UUID }}</span>
            </mat-option>
          }
        </mat-select>
        @if (state.problemOf('backend.existing'); as problem) {
          <mat-hint class="problem">{{ problem }}</mat-hint>
        } @else {
          <mat-hint>A generated file refers to it by name</mat-hint>
        }
      </mat-form-field>
    } @else {
      <mat-form-field>
        <mat-label>Name</mat-label>
        <input
          matInput
          name="backendName"
          flockProposedName
          autocomplete="off"
          [ngModel]="backend().name"
          (ngModelChange)="update({ name: $event })"
        />
        @if (state.problemOf('backend.name'); as problem) {
          <mat-hint class="problem">{{ problem }}</mat-hint>
        } @else {
          <mat-hint>Unique among the backends of the controller</mat-hint>
        }
      </mat-form-field>

      <div class="group">
        <span class="group-label">Endpoints</span>
        <flock-choice-cards
          label="Endpoints"
          [choices]="discoveryCards"
          [value]="backend().discovery"
          (picked)="pickDiscovery($event)"
        />
      </div>

      @if (backend().discovery === 'list') {
        <div class="group">
          @for (endpoint of backend().endpoints; track $index) {
            <div class="endpoint">
              <mat-form-field class="ip">
                <mat-label>IP</mat-label>
                <input
                  matInput
                  autocomplete="off"
                  [name]="'ip' + $index"
                  [ngModel]="endpoint.ip"
                  (ngModelChange)="updateEndpoint($index, { ip: $event })"
                />
                @if (state.problemOf('backend.endpoints.' + $index + '.ip'); as problem) {
                  <mat-hint class="problem">{{ problem }}</mat-hint>
                }
              </mat-form-field>
              <mat-form-field class="number">
                <mat-label>Port</mat-label>
                <input
                  matInput
                  type="number"
                  min="1"
                  max="65535"
                  step="1"
                  [name]="'port' + $index"
                  [ngModel]="endpoint.port || null"
                  (ngModelChange)="updateEndpoint($index, { port: numberOf($event) })"
                />
                @if (state.problemOf('backend.endpoints.' + $index + '.port'); as problem) {
                  <mat-hint class="problem">{{ problem }}</mat-hint>
                }
              </mat-form-field>
              <mat-form-field class="number">
                <mat-label>Weight</mat-label>
                <input
                  matInput
                  type="number"
                  min="0"
                  step="1"
                  [name]="'weight' + $index"
                  [ngModel]="endpoint.weight || null"
                  (ngModelChange)="updateEndpoint($index, { weight: numberOf($event) })"
                />
              </mat-form-field>
              <button
                mat-icon-button
                type="button"
                [attr.aria-label]="'Remove endpoint ' + ($index + 1)"
                (click)="removeEndpoint($index)"
              >
                <mat-icon>delete</mat-icon>
              </button>
            </div>
          }
          @if (state.problemOf('backend.endpoints'); as problem) {
            <p class="problem">
              <mat-icon>error</mat-icon>
              {{ problem }}
            </p>
          }
          <button mat-stroked-button type="button" (click)="addEndpoint()">
            <mat-icon>add</mat-icon>
            Add an endpoint
          </button>
        </div>
      } @else if (backend().discovery === 'dns') {
        <div class="endpoint">
          <mat-form-field class="ip">
            <mat-label>Host</mat-label>
            <input
              matInput
              name="dnsHost"
              autocomplete="off"
              placeholder="web.service.internal"
              [ngModel]="backend().dns.host"
              (ngModelChange)="updateDns({ host: $event })"
            />
            @if (state.problemOf('backend.dns.host'); as problem) {
              <mat-hint class="problem">{{ problem }}</mat-hint>
            } @else {
              <mat-hint>Resolved again every 5s, each address is an endpoint</mat-hint>
            }
          </mat-form-field>
          <mat-form-field class="number">
            <mat-label>Port</mat-label>
            <input
              matInput
              type="number"
              min="1"
              max="65535"
              step="1"
              name="dnsPort"
              [ngModel]="backend().dns.port || null"
              (ngModelChange)="updateDns({ port: numberOf($event) })"
            />
            @if (state.problemOf('backend.dns.port'); as problem) {
              <mat-hint class="problem">{{ problem }}</mat-hint>
            }
          </mat-form-field>
        </div>
      } @else {
        <mat-form-field>
          <mat-label>URL</mat-label>
          <input
            matInput
            name="edsUrl"
            autocomplete="off"
            placeholder="grpc://vigie:8080"
            [ngModel]="backend().eds.url"
            (ngModelChange)="updateEds({ url: $event })"
          />
          @if (state.problemOf('backend.eds.url'); as problem) {
            <mat-hint class="problem">{{ problem }}</mat-hint>
          } @else {
            <mat-hint>http, https, grpc or grpcs, asked again every 5s</mat-hint>
          }
        </mat-form-field>
        <mat-form-field>
          <mat-label>Service name</mat-label>
          <input
            matInput
            name="edsServiceName"
            autocomplete="off"
            [ngModel]="backend().eds.serviceName"
            (ngModelChange)="updateEds({ serviceName: $event })"
          />
          <mat-hint>The service whose endpoints the server gives</mat-hint>
        </mat-form-field>
        @if (edsHasUser()) {
          <div class="eds-password">
            <span class="eds-password-label">Password, from a secret</span>
            <flock-secret-ref-picker
              [value]="backend().eds.passwordRef"
              (picked)="updateEds({ passwordRef: $event })"
              (cleared)="updateEds({ passwordRef: '' })"
            />
          </div>
        }
      }

      @if (canUseTls()) {
        <mat-slide-toggle
          name="backendTls"
          [checked]="backend().tls"
          (change)="update({ tls: $event.checked })"
        >
          TLS to the endpoints
        </mat-slide-toggle>
      }

      <div class="group">
        <span class="group-label">Health check</span>
        <mat-button-toggle-group
          name="healthType"
          aria-label="Health check"
          [value]="backend().healthCheck.type"
          (change)="updateHealth({ type: $event.value })"
        >
          @for (type of healthTypes(); track type) {
            <mat-button-toggle [value]="type">{{ type }}</mat-button-toggle>
          }
        </mat-button-toggle-group>
        @if (backend().healthCheck.type === 'http') {
          <mat-form-field>
            <mat-label>Path</mat-label>
            <input
              matInput
              name="healthPath"
              autocomplete="off"
              [ngModel]="backend().healthCheck.path"
              (ngModelChange)="updateHealth({ path: $event })"
            />
            @if (state.problemOf('backend.healthCheck.path'); as problem) {
              <mat-hint class="problem">{{ problem }}</mat-hint>
            } @else {
              <mat-hint>Asked by the check, which wants a 2xx</mat-hint>
            }
          </mat-form-field>
        }
      </div>
      <p class="note">
        The controller gives the health check its interval and thresholds. Edit the backend once
        created, or the generated file, for anything else.
      </p>
    }

    <div class="actions">
      <button mat-button type="button" (click)="state.step.set(steps.vs)">Back</button>
      <span class="spacer"></span>
      <button
        mat-flat-button
        type="button"
        [disabled]="!state.backendDone()"
        (click)="state.step.set(steps.review)"
      >
        Next
      </button>
    </div>
  `,
  styleUrl: './step.scss',
  styles: `
    .eds-password {
      display: flex;
      flex-direction: column;
      gap: 4px;
    }
    .eds-password-label {
      color: var(--mat-sys-on-surface-variant);
    }
    .uuid {
      font-family: var(--flock-mono);
      font-size: 12px;
      color: var(--mat-sys-on-surface-variant);
    }
    mat-button-toggle-group {
      align-self: flex-start;
    }
    .endpoint {
      display: flex;
      flex-wrap: wrap;
      align-items: baseline;
      gap: 8px;
      .ip {
        flex: 1 1 200px;
      }
      .number {
        width: 110px;
      }
    }
  `,
})
export class BackendStep {
  protected state = inject(BuilderState);
  protected readonly steps = steps;

  protected readonly discoveryCards = discoveryCards;

  protected backend = this.state.newBackend;
  protected hasExisting = computed(() => this.state.controllerBackends().length > 0);
  private protocol = computed(() => this.state.vs().protocol);
  protected canUseTls = computed(() => this.protocol() !== 'tcp' && this.protocol() !== 'udp');
  // a udp check has no connection to send an http request on
  protected healthTypes = computed<HealthCheckType[]>(() =>
    this.protocol() === 'udp' ? ['none', 'tcp'] : ['none', 'tcp', 'http'],
  );

  protected update(change: Partial<BackendInput>) {
    this.backend.update((backend) => ({ ...backend, ...change }));
  }

  protected pickDiscovery(value: string) {
    this.update({ discovery: value as Discovery });
  }

  protected updateDns(change: Partial<BackendInput['dns']>) {
    this.update({ dns: { ...this.backend().dns, ...change } });
  }

  // basic auth needs a user, so the password is asked once the url names one
  protected readonly edsHasUser = computed(
    () => edsUrl(this.backend().eds.url)?.userInfo !== undefined,
  );

  protected updateEds(change: Partial<BackendInput['eds']>) {
    this.update({ eds: { ...this.backend().eds, ...change } });
  }

  protected updateHealth(change: Partial<BackendInput['healthCheck']>) {
    this.update({ healthCheck: { ...this.backend().healthCheck, ...change } });
  }

  protected addEndpoint() {
    this.update({ endpoints: [...this.backend().endpoints, { ip: '', port: 0, weight: 0 }] });
  }

  protected updateEndpoint(index: number, change: Partial<Endpoint>) {
    const endpoints = this.backend().endpoints.map((endpoint, i) =>
      i === index ? { ...endpoint, ...change } : endpoint,
    );
    this.update({ endpoints });
  }

  protected removeEndpoint(index: number) {
    this.update({ endpoints: this.backend().endpoints.filter((_, i) => i !== index) });
  }

  // an emptied number field is 0, which the check of a port refuses and a weight leaves to the default
  protected numberOf(value: unknown): number {
    const isNumber = typeof value === 'number' && Number.isFinite(value);
    return isNumber ? value : 0;
  }
}
