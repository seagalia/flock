import { Component, signal, ViewChild } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { MatTableModule } from '@angular/material/table';
import { provideRouter } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { AgentSchema } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { Api, ListPage } from '../core/api';
import { Resource } from '../core/resources';
import { MenuItem } from './resource-menu';
import { Column, ExpandedRow, ResourceTable } from './resource-table';

@Component({
  imports: [ResourceTable, MatTableModule],
  template: `
    <flock-resource-table [columns]="columns" [load]="load">
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let vip">{{ vip.name }}</td>
      </ng-container>
    </flock-resource-table>
  `,
})
class Host {
  readonly columns: Column[] = [{ key: 'name', label: 'Name' }];
  readonly pages = signal<ListPage[]>([]);
  readonly load = async (page: ListPage): Promise<Resource[]> => {
    this.pages.update((pages) => [...pages, page]);
    return Array.from({ length: page.limit }, (_, index) =>
      create(VIPSchema, {
        UUID: `uuid-${page.offset + index}`,
        name: `vip-${page.offset + index}`,
      }),
    );
  };
}

// lists vip-0 alone when searched, vip-0 and vip-1 otherwise, or only vip-0 with one=true
@Component({
  imports: [ResourceTable, MatTableModule, ExpandedRow],
  template: `
    <flock-resource-table [columns]="columns" [load]="load">
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let vip">{{ vip.name }}</td>
      </ng-container>
      <ng-template flockExpandedRow let-vip>details of {{ vip.name }}</ng-template>
    </flock-resource-table>
  `,
})
class ExpandingHost {
  readonly columns: Column[] = [{ key: 'name', label: 'Name' }];
  one = false;
  readonly load = async (page: ListPage): Promise<Resource[]> => {
    const searched = page.filters.length > 0;
    const count = searched || this.one ? 1 : 2;
    return Array.from({ length: count }, (_, index) =>
      create(VIPSchema, { UUID: `uuid-${index}`, name: `vip-${index}` }),
    );
  };
}

@Component({
  imports: [ResourceTable, MatTableModule, ExpandedRow],
  template: `
    <flock-resource-table [columns]="columns" [load]="load" [hiddenColumns]="[]">
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let vip">{{ vip.name }}</td>
      </ng-container>
      <ng-container matColumnDef="uuid">
        <th mat-header-cell *matHeaderCellDef>UUID</th>
        <td mat-cell *matCellDef="let vip">{{ vip.UUID }}</td>
      </ng-container>
      <ng-template flockExpandedRow let-vip>details of {{ vip.name }}</ng-template>
    </flock-resource-table>
  `,
})
class OpeningHost {
  readonly columns: Column[] = [
    { key: 'name', label: 'Name' },
    { key: 'uuid', label: 'UUID' },
  ];
  readonly pages: ListPage[] = [];
  @ViewChild(ResourceTable) table!: ResourceTable;
  readonly load = async (page: ListPage): Promise<Resource[]> => {
    this.pages.push(page);
    return [create(VIPSchema, { UUID: 'uuid-0', name: 'vip-0' })];
  };
}

// lists vip-0 with an Edit, a greyed Delete and a Clone in its menu
@Component({
  imports: [ResourceTable, MatTableModule, ExpandedRow],
  template: `
    <flock-resource-table [columns]="columns" [load]="load" [extraItems]="items">
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let vip">{{ vip.name }}</td>
      </ng-container>
      <ng-template flockExpandedRow let-vip>details of {{ vip.name }}</ng-template>
    </flock-resource-table>
  `,
})
class ActionsHost {
  readonly columns: Column[] = [{ key: 'name', label: 'Name' }];
  edited: string[] = [];
  deleted: string[] = [];
  readonly load = async (): Promise<Resource[]> => [
    create(VIPSchema, { UUID: 'uuid-0', name: 'vip-0' }),
  ];
  readonly items = (row: Resource): MenuItem[] => [
    { label: 'Edit', icon: 'edit', tone: 'primary', run: () => this.edited.push(row.name) },
    {
      label: 'Delete',
      icon: 'delete',
      tone: 'danger',
      disabled: 'Locked',
      run: () => this.deleted.push(row.name),
    },
    { label: 'Clone', icon: 'content_copy', run: () => undefined },
  ];
}

// lists vip-0, provisioned by the agent p1, and vip-1 that no provisioner touched
@Component({
  imports: [ResourceTable, MatTableModule, ExpandedRow],
  template: `
    <flock-resource-table [columns]="columns" [load]="load">
      <ng-container matColumnDef="name">
        <th mat-header-cell *matHeaderCellDef>Name</th>
        <td mat-cell *matCellDef="let vip">{{ vip.name }}</td>
      </ng-container>
      <ng-template flockExpandedRow let-vip>details of {{ vip.name }}</ng-template>
    </flock-resource-table>
  `,
})
class ProvisionedHost {
  readonly columns: Column[] = [{ key: 'name', label: 'Name' }];
  readonly load = async (): Promise<Resource[]> => [
    create(VIPSchema, { UUID: 'uuid-0', name: 'vip-0', metadata: { 'flock.provision.by': 'p1' } }),
    create(VIPSchema, { UUID: 'uuid-1', name: 'vip-1' }),
  ];
}

async function search(fixture: { nativeElement: HTMLElement; whenStable(): Promise<unknown> }) {
  const input = fixture.nativeElement.querySelector('input') as HTMLInputElement;
  input.value = 'vip-0';
  input.dispatchEvent(new Event('input'));
  input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
  await fixture.whenStable();
}

describe('ResourceTable', () => {
  it('opens the only row a search finds', async () => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const fixture = TestBed.createComponent(ExpandingHost);
    await fixture.whenStable();
    expect(fixture.nativeElement.textContent).not.toContain('details of');

    await search(fixture);

    expect(fixture.nativeElement.textContent).toContain('details of vip-0');
  });

  it('keeps a lone row closed without a search', async () => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const fixture = TestBed.createComponent(ExpandingHost);
    fixture.componentInstance.one = true;
    await fixture.whenStable();

    expect(fixture.nativeElement.querySelectorAll('tr.element-row').length).toBe(1);
    expect(fixture.nativeElement.textContent).not.toContain('details of');
  });

  it('lists the first page with the cells given by the page', async () => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const fixture = TestBed.createComponent(Host);
    await fixture.whenStable();
    fixture.detectChanges();

    const page = fixture.componentInstance.pages()[0];
    expect(page.limit).toBe(10);
    expect(page.offset).toBe(0);
    expect(page.filters).toEqual([]);

    const rows = fixture.nativeElement.querySelectorAll('tr.element-row');
    expect(rows.length).toBe(10);
    expect(rows[0].textContent).toContain('vip-0');
  });

  it('expands a row on a click of any cell, the name one included', async () => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const fixture = TestBed.createComponent(OpeningHost);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
    const element = fixture.nativeElement as HTMLElement;

    (element.querySelector('td.mat-column-name') as HTMLElement).click();
    await fixture.whenStable();
    fixture.detectChanges();
    expect(element.textContent).toContain('details of vip-0');
  });

  it('shows the Edit and Delete of the menu as buttons in the expanded row', async () => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const fixture = TestBed.createComponent(ActionsHost);
    await fixture.whenStable();
    const element = fixture.nativeElement as HTMLElement;

    (element.querySelector('td.mat-column-name') as HTMLElement).click();
    await fixture.whenStable();
    const buttons = [...element.querySelectorAll<HTMLButtonElement>('.row-actions button')];
    expect(buttons.map((button) => button.textContent?.trim())).toEqual([
      'edit Edit',
      'delete Delete',
    ]);

    buttons[0].click();
    buttons[1].click();
    expect(fixture.componentInstance.edited).toEqual(['vip-0']);
    expect(fixture.componentInstance.deleted).toEqual([]);
    expect(buttons[1].getAttribute('aria-disabled')).toBe('true');
  });

  it('shows who provisioned the resource in its expanded row', async () => {
    const readAgent = vi.fn(async () => ({
      agent: create(AgentSchema, { UUID: 'p1', name: 'acme' }),
      instanceUuid: 'inst-1',
    }));
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: Api, useValue: { lookup: { readAgent } } }],
    });
    const fixture = TestBed.createComponent(ProvisionedHost);
    await fixture.whenStable();
    const rows = fixture.nativeElement.querySelectorAll('tr.element-row');

    (rows[0] as HTMLElement).click();
    (rows[1] as HTMLElement).click();
    await fixture.whenStable();
    await new Promise((resolve) => setTimeout(resolve));
    await fixture.whenStable();

    const details = [...fixture.nativeElement.querySelectorAll('tr.detail-row')].map(
      (row: Element) => row.textContent ?? '',
    );
    expect(details[0]).toContain('Provisioned by');
    expect(details[0]).toContain('acme');
    expect(details[1]).not.toContain('Provisioned by');
    expect(readAgent).toHaveBeenCalledTimes(1);
  });

  it('reload lists the current page again', async () => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const fixture = TestBed.createComponent(OpeningHost);
    fixture.detectChanges();
    await fixture.whenStable();

    fixture.componentInstance.table.reload();
    await fixture.whenStable();

    const pages = fixture.componentInstance.pages;
    expect(pages.length).toBe(2);
    expect(pages[1]).toEqual(pages[0]);
  });
});
