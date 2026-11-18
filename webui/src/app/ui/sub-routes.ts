import { Component, input } from '@angular/core';
import { MatDividerModule } from '@angular/material/divider';
import { MatTableModule } from '@angular/material/table';
import { Route } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { routeActionLabel, routeMatchLabel } from '../core/display';
import { RouterLink } from '@angular/router';
import { kinds } from '../core/resources';

@Component({
  selector: 'flock-sub-routes',
  imports: [MatTableModule, MatDividerModule, RouterLink],
  template: `
    <h2>Sub Routes</h2>
    <mat-divider />
    @if (routes().length > 0) {
      <div class="bordered">
        <table mat-table [dataSource]="routes()">
          <ng-container matColumnDef="name">
            <th mat-header-cell *matHeaderCellDef>Name</th>
            <td mat-cell *matCellDef="let route">{{ route.name }}</td>
          </ng-container>
          <ng-container matColumnDef="match">
            <th mat-header-cell *matHeaderCellDef>Match</th>
            <td mat-cell *matCellDef="let route">{{ matchLabel(route) }}</td>
          </ng-container>
          <ng-container matColumnDef="action">
            <th mat-header-cell *matHeaderCellDef>Action</th>
            <td mat-cell *matCellDef="let route">{{ actionLabel(route) }}</td>
          </ng-container>
          <ng-container matColumnDef="backend">
            <th mat-header-cell *matHeaderCellDef>Backend</th>
            <td mat-cell *matCellDef="let route">
              @if (backend(route) !== undefined) {
                <div class="address">
                  <a
                    [routerLink]="backendLink"
                    [queryParams]="{ uuid: backend(route) }"
                    (click)="$event.stopPropagation()"
                    >{{ backend(route) }}</a
                  >
                </div>
              } @else {
                None
              }
            </td>
          </ng-container>
          <tr mat-header-row *matHeaderRowDef="columns"></tr>
          <tr mat-row *matRowDef="let row; columns: columns"></tr>
        </table>
      </div>
    } @else {
      <p>No sub routes set.</p>
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
export class SubRoutes {
  readonly routes = input.required<Route[]>();
  protected readonly columns = ['name', 'match', 'action', 'backend'];
  protected readonly matchLabel = routeMatchLabel;
  protected readonly actionLabel = routeActionLabel;
  protected readonly backendLink = kinds.backend.link;
  protected backend = (route: Route) => {
    if (route.action.case === 'backendUuid') {
      return route.action.value;
    }
    return undefined;
  };
}
