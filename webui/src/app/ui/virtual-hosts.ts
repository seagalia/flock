import { Component, input } from '@angular/core';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { VirtualHost } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { SubRoutes } from './sub-routes';

// each virtual host is followed by its sub routes: a virtual host is only read through them
@Component({
  selector: 'flock-virtual-hosts',
  imports: [MatTableModule, MatDividerModule, SubRoutes],
  template: `
    <h2>Virtual Hosts</h2>
    <mat-divider />
    @if (virtualHosts().length > 0) {
      <div class="bordered">
        <table mat-table [dataSource]="virtualHosts()" multiTemplateDataRows>
          <ng-container matColumnDef="domains">
            <th mat-header-cell *matHeaderCellDef>Domains</th>
            <td mat-cell *matCellDef="let host">
              <div class="chips">
                @for (domain of host.domains; track domain) {
                  <span class="chip unknown">{{ domain === '*' ? 'any domain' : domain }}</span>
                }
              </div>
            </td>
          </ng-container>
          <ng-container matColumnDef="pathPrefix">
            <th mat-header-cell *matHeaderCellDef>Path Prefix</th>
            <td mat-cell *matCellDef="let host">{{ host.PathPrefix || '/' }}</td>
          </ng-container>
          <ng-container matColumnDef="sni">
            <th mat-header-cell *matHeaderCellDef>SNI Match</th>
            <td mat-cell *matCellDef="let host">
              @for (name of serverNames(host); track name) {
                <div>{{ name }}</div>
              } @empty {
                No SNI match
              }
            </td>
          </ng-container>
          <ng-container matColumnDef="subRoutes">
            <td mat-cell *matCellDef="let host" [attr.colspan]="columns.length">
              <flock-sub-routes [routes]="host.routes" />
            </td>
          </ng-container>
          <tr mat-header-row *matHeaderRowDef="columns"></tr>
          <tr mat-row *matRowDef="let row; columns: columns"></tr>
          <tr mat-row *matRowDef="let row; columns: ['subRoutes']"></tr>
        </table>
      </div>
    } @else {
      <p>No virtual host set.</p>
    }
  `,
  styles: `
    .bordered {
      border: 1px solid var(--flock-border);
      border-radius: 8px;
      overflow: auto;
      margin: 10px 0 20px;
    }
  `,
})
export class VirtualHosts {
  readonly virtualHosts = input.required<VirtualHost[]>();
  protected readonly columns = ['domains', 'pathPrefix', 'sni'];

  protected serverNames = (host: VirtualHost) => host.tlsOptions.flatMap((tls) => tls.serverNames);
}
