import { Component, effect, inject, input, signal } from '@angular/core';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { RouterLink } from '@angular/router';
import { Backend, BackendEndpointStats } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { Api } from '../core/api';
import { kinds } from '../core/resources';
import { BackendConfig } from './backend-config';

// BackendStats is the card of a backend with the state of its endpoints
@Component({
  selector: 'flock-backend-stats',
  imports: [MatTableModule, MatDividerModule, RouterLink, BackendConfig],
  template: `
    @if (backend(); as backend) {
      <div class="card">
        <h2>
          Backend
          <a [routerLink]="backendLink" [queryParams]="{ uuid: backend.UUID }">{{
            backend.name
          }}</a>
        </h2>
        <h3>Backend Configuration</h3>
        <mat-divider />
        <flock-backend-config [backend]="backend" />
        @if (endpointStats(); as stats) {
          <h3>Endpoints States</h3>
          <table mat-table [dataSource]="stats.endpointStats">
            <ng-container matColumnDef="endpoint">
              <th mat-header-cell *matHeaderCellDef>Endpoint</th>
              <td mat-cell *matCellDef="let endpoint">
                {{ endpoint.ipAddr?.ip }}:{{ endpoint.ipAddr?.port }}
              </td>
            </ng-container>
            <ng-container matColumnDef="state">
              <th mat-header-cell *matHeaderCellDef>State</th>
              <td mat-cell *matCellDef="let endpoint">
                <span [class]="endpoint.healthy ? 'chip ok' : 'chip error'">
                  {{ endpoint.healthy ? 'Healthy' : 'Unhealthy' }}
                </span>
              </td>
            </ng-container>
            <ng-container matColumnDef="error">
              <th mat-header-cell *matHeaderCellDef>Error</th>
              <td mat-cell *matCellDef="let endpoint">{{ endpoint.lastError }}</td>
            </ng-container>
            <tr mat-header-row *matHeaderRowDef="columns"></tr>
            <tr mat-row *matRowDef="let row; columns: columns"></tr>
          </table>
        }
      </div>
    }
  `,
  styles: `
    .card {
      border: 1px solid var(--flock-border);
      border-radius: 12px;
      background: var(--mat-sys-surface);
      margin: 15px 1%;
      padding: 15px;
    }
  `,
})
export class BackendStats {
  readonly backendUuid = input.required<string>();
  // null when no agent reported the endpoints of the backend
  readonly endpointStats = input<BackendEndpointStats | null>(null);

  private api = inject(Api);
  protected backend = signal<Backend | null>(null);
  protected readonly columns = ['endpoint', 'state', 'error'];
  protected readonly backendLink = kinds.backend.link;

  constructor() {
    effect(() => {
      this.api.backend
        .readBackend({ UUID: this.backendUuid() })
        .then((resp) => this.backend.set(resp.backend ?? null));
    });
  }
}
