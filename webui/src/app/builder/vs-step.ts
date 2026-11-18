import { COMMA, ENTER, SPACE } from '@angular/cdk/keycodes';
import { Component, computed, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatChipInputEvent, MatChipsModule } from '@angular/material/chips';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectChange, MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Choice, ChoiceCards } from '../ui/choice-cards';
import { BuilderState, createChoice, steps, VsInput } from './builder-state';
import { Protocol } from './documents';
import { ProposedName } from '../ui/proposed-name';

const protocolCards: Choice[] = [
  { value: 'http', title: 'http', line: 'Web traffic by domain' },
  { value: 'https', title: 'https', line: 'Web traffic by domain, TLS terminated by flock' },
  { value: 'tls', title: 'tls', line: 'Any TLS service routed on its SNI' },
  { value: 'tcp', title: 'tcp', line: 'A port, any TCP service' },
  { value: 'udp', title: 'udp', line: 'A port, any UDP service' },
];

// VsStep fills the virtual server of the run, only the fields its protocol uses are shown
@Component({
  selector: 'flock-vs-step',
  imports: [
    ProposedName,
    ChoiceCards,
    FormsModule,
    MatButtonModule,
    MatChipsModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatSlideToggleModule,
    MatTooltipModule,
  ],
  template: `
    <flock-choice-cards
      label="Protocol"
      [choices]="protocolCards"
      [value]="vs().protocol"
      (picked)="pickProtocol($event)"
    />

    <mat-form-field>
      <mat-label>Name</mat-label>
      <input
        matInput
        name="vsName"
        flockProposedName
        autocomplete="off"
        [ngModel]="vs().name"
        (ngModelChange)="update({ name: $event })"
      />
      @if (state.problemOf('vs.name'); as problem) {
        <mat-hint class="problem">{{ problem }}</mat-hint>
      } @else {
        <mat-hint>Unique among the virtual servers of the controller</mat-hint>
      }
    </mat-form-field>

    <mat-form-field>
      <mat-label>Port</mat-label>
      <input
        matInput
        type="number"
        name="vsPort"
        min="1"
        max="65535"
        step="1"
        [ngModel]="vs().port || null"
        (ngModelChange)="update({ port: numberOf($event) })"
      />
      @if (state.problemOf('vs.port'); as problem) {
        <mat-hint class="problem">{{ problem }}</mat-hint>
      }
    </mat-form-field>

    @if (vs().protocol === 'tls') {
      <mat-slide-toggle
        name="passthrough"
        [checked]="vs().passthrough"
        (change)="update({ passthrough: $event.checked })"
      >
        Pass TLS through, the endpoints terminate it with their own certificate
      </mat-slide-toggle>
    }

    @if (hasDomains()) {
      <mat-form-field>
        <mat-label>Domains</mat-label>
        <mat-chip-grid #domainGrid aria-label="Domains">
          @for (domain of vs().domains; track domain) {
            <mat-chip-row (removed)="removeDomain(domain)">
              {{ domain === '*' ? 'any domain' : domain }}
              <button matChipRemove type="button" [attr.aria-label]="'Remove ' + domain">
                <mat-icon>cancel</mat-icon>
              </button>
            </mat-chip-row>
          }
          <input
            name="domain"
            autocomplete="off"
            placeholder="api.example.com"
            [matChipInputFor]="domainGrid"
            [matChipInputSeparatorKeyCodes]="separatorKeys"
            [matChipInputAddOnBlur]="true"
            (matChipInputTokenEnd)="addDomain($event)"
          />
        </mat-chip-grid>
        <mat-hint>{{ domainsHint() }}</mat-hint>
      </mat-form-field>
      <!-- https and tls domains are TLS server names too, where a bare * is invalid -->
      @if (vs().protocol === 'http') {
        <button
          mat-stroked-button
          type="button"
          class="any-domain"
          [disabled]="isAnyDomain()"
          (click)="update({ domains: ['*'] })"
        >
          Any domain (*)
        </button>
      }
    }

    @if (needsCertificate()) {
      <mat-form-field>
        <mat-label>Certificate</mat-label>
        <mat-select
          name="certificate"
          [value]="vs().certificateUuid"
          (selectionChange)="pickCertificate($event)"
        >
          <mat-option
            [value]="createChoice"
            [disabled]="!!state.createGreyed('certificate')"
            [matTooltip]="state.createGreyed('certificate')"
          >
            <mat-icon>add</mat-icon>
            Create a certificate
          </mat-option>
          @for (certificate of state.certificates(); track certificate.UUID) {
            <mat-option [value]="certificate.UUID">
              {{ certificate.name }} <span class="uuid">{{ certificate.UUID }}</span>
            </mat-option>
          }
        </mat-select>
        @if (state.problemOf('vs.certificate'); as problem) {
          <mat-hint class="problem">{{ problem }}</mat-hint>
        } @else {
          <mat-hint>The certificate served to the clients</mat-hint>
        }
      </mat-form-field>
    }

    <div class="actions">
      <button mat-button type="button" (click)="state.step.set(steps.vip)">Back</button>
      <span class="spacer"></span>
      <button
        mat-flat-button
        type="button"
        [disabled]="!state.vsDone()"
        (click)="state.step.set(steps.backend)"
      >
        Next
      </button>
    </div>
  `,
  styleUrl: './step.scss',
  styles: `
    .any-domain {
      align-self: flex-start;
      margin-top: -8px;
    }
    .uuid {
      font-family: var(--flock-mono);
      font-size: 12px;
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class VsStep {
  protected state = inject(BuilderState);
  protected readonly steps = steps;
  protected readonly protocolCards = protocolCards;
  protected readonly separatorKeys = [ENTER, COMMA, SPACE];
  protected readonly createChoice = createChoice;

  protected vs = this.state.vs;

  protected pickProtocol(value: string) {
    this.state.setProtocol(value as Protocol);
  }
  protected isWeb = computed(() => this.vs().protocol === 'http' || this.vs().protocol === 'https');
  protected isPassthrough = computed(() => this.vs().protocol === 'tls' && this.vs().passthrough);
  protected hasDomains = computed(() => this.isWeb() || this.isPassthrough());
  protected needsCertificate = computed(() => {
    const isTerminatedTls = this.vs().protocol === 'tls' && !this.vs().passthrough;
    return this.vs().protocol === 'https' || isTerminatedTls;
  });
  protected isAnyDomain = computed(() => {
    const domains = this.vs().domains;
    return domains.length === 1 && domains[0] === '*';
  });
  protected domainsHint = computed(() =>
    this.isPassthrough()
      ? 'The SNI server names routed to the backend'
      : 'The hosts the virtual server answers, every host when empty',
  );

  protected update(change: Partial<VsInput>) {
    this.vs.update((vs) => ({ ...vs, ...change }));
  }

  protected pickCertificate(change: MatSelectChange) {
    if (change.value !== createChoice) {
      this.update({ certificateUuid: change.value });
      return;
    }
    // the create entry is not a value, the select goes back to the certificate it showed
    change.source.value = this.vs().certificateUuid;
    this.state.createCertificate();
  }

  protected addDomain(event: MatChipInputEvent) {
    const domain = event.value.trim();
    event.chipInput.clear();
    const isNew = domain !== '' && !this.vs().domains.includes(domain);
    if (isNew) {
      this.update({ domains: [...this.vs().domains, domain] });
    }
  }

  protected removeDomain(domain: string) {
    this.update({ domains: this.vs().domains.filter((candidate) => candidate !== domain) });
  }

  // an emptied number field is 0, which the check of the port refuses
  protected numberOf(value: unknown): number {
    const isNumber = typeof value === 'number' && Number.isFinite(value);
    return isNumber ? value : 0;
  }
}
