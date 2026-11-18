import { DatePipe } from '@angular/common';
import { Component, effect, input, signal } from '@angular/core';
import { Certificate } from '../../gen/flock/api/config/cert/v1/cert_pb';
import { certificateChainDetails, CertificateDetails } from '../core/certificate-details';

// CertificateDetailsPanel shows what the certificates of a chain say, next to the chain as text
@Component({
  selector: 'flock-certificate-details',
  imports: [DatePipe],
  template: `
    @if (certificate().domainNames.length > 0 || certificate().ca) {
      <section class="card">
        <h3>In flock</h3>
        <dl>
          @if (certificate().domainNames.length > 0) {
            <dt>Domain names</dt>
            <dd>
              @for (domain of certificate().domainNames; track domain) {
                <span class="value">{{ domain }}</span>
              }
            </dd>
          }
          <dt>CA</dt>
          <dd>{{ certificate().ca ? 'set, client certificates are verified' : 'not set' }}</dd>
        </dl>
      </section>
    }

    @for (cert of chain(); track cert.sha256Fingerprint; let index = $index) {
      <section class="card">
        <h3>
          {{ role(cert, index) }}
          @if (chain().length > 1) {
            <span class="position">{{ index + 1 }} of {{ chain().length }}</span>
          }
        </h3>
        <dl>
          <dt>Subject</dt>
          <dd class="value">{{ cert.subject }}</dd>
          <dt>Issuer</dt>
          <dd class="value">{{ cert.selfSigned ? 'self-signed' : cert.issuer }}</dd>
          @if (cert.dnsNames.length > 0) {
            <dt>DNS names</dt>
            <dd>
              @for (name of cert.dnsNames; track name) {
                <span class="value">{{ name }}</span>
              }
            </dd>
          }
          @if (cert.ipAddresses.length > 0) {
            <dt>IP addresses</dt>
            <dd>
              @for (ip of cert.ipAddresses; track ip) {
                <span class="value">{{ ip }}</span>
              }
            </dd>
          }
          @if (cert.emails.length > 0) {
            <dt>Emails</dt>
            <dd>
              @for (email of cert.emails; track email) {
                <span class="value">{{ email }}</span>
              }
            </dd>
          }
          @if (cert.uris.length > 0) {
            <dt>URIs</dt>
            <dd>
              @for (uri of cert.uris; track uri) {
                <span class="value">{{ uri }}</span>
              }
            </dd>
          }
          <dt>Valid from</dt>
          <dd>{{ cert.notBefore | date: 'y-MM-dd HH:mm:ss zzzz' }}</dd>
          <dt>Valid until</dt>
          <dd>{{ cert.notAfter | date: 'y-MM-dd HH:mm:ss zzzz' }}</dd>
          <dt>Public key</dt>
          <dd>{{ cert.publicKey }}</dd>
          <dt>Signature</dt>
          <dd>{{ cert.signatureAlgorithm }}</dd>
          @if (cert.isCa) {
            <dt>Path length</dt>
            <dd>{{ cert.pathLength ?? 'no limit' }}</dd>
          }
          @if (cert.keyUsages.length > 0) {
            <dt>Key usage</dt>
            <dd>{{ cert.keyUsages.join(', ') }}</dd>
          }
          @if (cert.extendedKeyUsages.length > 0) {
            <dt>Extended key usage</dt>
            <dd>{{ cert.extendedKeyUsages.join(', ') }}</dd>
          }
          <dt>Serial number</dt>
          <dd class="value mono">{{ cert.serialNumber }}</dd>
          <dt>SHA-256</dt>
          <dd class="value mono">{{ cert.sha256Fingerprint }}</dd>
        </dl>
      </section>
    } @empty {
      @if (loaded()) {
        <p>The certificate chain could not be read.</p>
      }
    }
  `,
  styles: `
    :host {
      display: flex;
      flex-direction: column;
      gap: 12px;
      min-width: 0;
    }
    .card {
      border: 1px solid var(--flock-border);
      border-radius: 8px;
      padding: 12px 15px;
    }
    h3 {
      margin: 0 0 8px;
    }
    .position {
      margin-left: 8px;
      font-size: 13px;
      font-weight: normal;
      color: var(--mat-sys-on-surface-variant);
    }
    dl {
      display: grid;
      grid-template-columns: max-content minmax(0, 1fr);
      gap: 6px 16px;
      margin: 0;
    }
    dt {
      color: var(--mat-sys-on-surface-variant);
    }
    dd {
      margin: 0;
      display: flex;
      flex-wrap: wrap;
      gap: 4px 12px;
    }
    .value {
      overflow-wrap: anywhere;
      user-select: all;
    }
    .mono {
      font-family: var(--flock-mono);
      font-size: 12px;
    }
  `,
})
export class CertificateDetailsPanel {
  readonly certificate = input.required<Certificate>();

  protected chain = signal<CertificateDetails[]>([]);
  protected loaded = signal(false);

  constructor() {
    effect(() => {
      const certChain = this.certificate().certChain;
      this.loaded.set(false);
      certificateChainDetails(certChain).then((chain) => {
        this.chain.set(chain);
        this.loaded.set(true);
      });
    });
  }

  // role names a certificate by its place in the chain, the first one is what the server presents
  protected role(cert: CertificateDetails, index: number): string {
    if (index === 0) {
      return 'Server certificate';
    }
    return cert.selfSigned ? 'Root CA' : 'Intermediate CA';
  }
}
