import { DatePipe } from '@angular/common';
import { Component, inject } from '@angular/core';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Certificate } from '../../gen/flock/api/config/cert/v1/cert_pb';
import { Api, ListPage } from '../core/api';
import { CertificateInfo, certificateInfo, expiryLabel } from '../core/certificate-details';
import { KindName } from '../core/resources';
import { LockBadge } from '../edit/lock-badge';
import { ResourceEditor } from '../edit/resource-editor';
import { CertificateDetailsPanel } from '../ui/certificate-details';
import { LinksPermissions } from '../ui/links-permissions';
import { Column, ExpandedRow, ResourceTable } from '../ui/resource-table';

const expiryWarningMs = 30 * 24 * 3600 * 1000;

@Component({
  selector: 'flock-certificates',
  imports: [
    ResourceEditor,
    LockBadge,
    MatTableModule,
    MatDividerModule,
    DatePipe,
    MatTooltipModule,
    ResourceTable,
    ExpandedRow,
    LinksPermissions,
    CertificateDetailsPanel,
  ],
  template: `
    <h1>Certificates Management</h1>
    <mat-divider />
    <flock-resource-table
      #table
      [columns]="columns"
      [load]="load"
      [selectable]="false"
      [extraItems]="editor.menuItems"
    >
      <flock-resource-editor
        tableControlsEnd
        #editor
        kind="certificate"
        (changed)="table.reload()"
      />
      <ng-container matColumnDef="uuid">
        <th mat-header-cell *matHeaderCellDef>UUID</th>
        <td mat-cell *matCellDef="let cert">{{ cert.UUID }}</td>
      </ng-container>
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let cert">
          {{ cert.name }}<flock-lock-badge [resource]="cert" />
        </td>
      </ng-container>
      <ng-container matColumnDef="cn">
        <th mat-header-cell *matHeaderCellDef>Common Name</th>
        <td mat-cell *matCellDef="let cert">
          {{ info(cert)?.commonName ?? 'unreadable certificate' }}
        </td>
      </ng-container>
      <ng-container matColumnDef="org">
        <th mat-header-cell *matHeaderCellDef>Organisation</th>
        <td mat-cell *matCellDef="let cert">{{ info(cert)?.organisation }}</td>
      </ng-container>
      <ng-container matColumnDef="validTo">
        <th mat-header-cell *matHeaderCellDef>Valid Until</th>
        <td mat-cell *matCellDef="let cert">
          @if (info(cert)?.validTo; as validTo) {
            <span
              [class]="'chip ' + (expiresSoon(cert) ? 'error' : 'ok')"
              [matTooltip]="validTo | date: 'd MMMM y, HH:mm (zzzz)'"
              >{{ expiry(validTo) }}</span
            >
          } @else {
            <span class="chip unknown">unreadable certificate</span>
          }
        </td>
      </ng-container>
      <ng-template flockExpandedRow let-cert>
        <div class="chain">
          <div>
            <h2>Certificate Chain</h2>
            <mat-divider />
            <pre class="pem"><code>{{ cert.certChain }}</code></pre>
          </div>
          <div class="details">
            <h2>Details</h2>
            <mat-divider />
            <flock-certificate-details [certificate]="cert" />
          </div>
        </div>
        <flock-links-permissions [resource]="cert" [linkedKinds]="linkedKinds" />
      </ng-template>
    </flock-resource-table>
  `,
  styles: `
    .chain {
      display: flex;
      flex-wrap: wrap;
      gap: 0 24px;
    }

    .details {
      flex: 1 1 420px;
      min-width: 0;

      flock-certificate-details {
        margin: 10px 0;
      }
    }

    .pem {
      background: var(--flock-code-bg);
      color: var(--flock-code-text);
      width: min(462px, 100%);
      height: 462px;
      padding: 15px;
      margin: 10px 0;
      border-radius: 8px;
      border: 1px solid var(--flock-border);
      overflow: auto;
      box-sizing: border-box;

      code {
        font-family: var(--flock-mono);
        font-size: 12px;
        white-space: pre-wrap;
        word-wrap: break-word;
      }
    }
  `,
})
export class Certificates {
  protected readonly columns: Column[] = [
    { key: 'uuid', label: 'UUID' },
    { key: 'name', label: 'Name' },
    { key: 'cn', label: 'Common Name' },
    { key: 'org', label: 'Organisation' },
    { key: 'validTo', label: 'Valid Until' },
  ];
  protected readonly linkedKinds: KindName[] = ['vs'];
  private api = inject(Api);
  // parsing a chain is not free, a row is drawn on every change detection
  private infos = new Map<string, CertificateInfo | null>();

  protected load = (page: ListPage) => this.api.list('certificate', page);

  protected info(cert: Certificate): CertificateInfo | null {
    if (!this.infos.has(cert.certChain)) {
      this.infos.set(cert.certChain, certificateInfo(cert.certChain));
    }
    return this.infos.get(cert.certChain) ?? null;
  }

  protected expiry(validTo: Date): string {
    return expiryLabel(validTo, new Date());
  }

  protected expiresSoon(cert: Certificate): boolean {
    const validTo = this.info(cert)?.validTo;
    return !validTo || validTo.getTime() - Date.now() < expiryWarningMs;
  }
}
