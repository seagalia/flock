import { SelectionModel } from '@angular/cdk/collections';
import { NgTemplateOutlet } from '@angular/common';
import {
  AfterContentInit,
  Component,
  computed,
  ContentChild,
  ContentChildren,
  DestroyRef,
  Directive,
  effect,
  inject,
  input,
  QueryList,
  signal,
  TemplateRef,
  untracked,
  ViewChild,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { MatColumnDef, MatTable, MatTableModule } from '@angular/material/table';
import { ActivatedRoute, Router } from '@angular/router';
import { ListPage } from '../core/api';
import { isUuid, nameOrUuidFilter, Resource, resourceId } from '../core/resources';
import { MetadataChips } from './metadata-chips';
import { ProvisionedBy, provisionerUuidOf } from './provisioned-by';
import { MenuItem, ResourceMenu } from './resource-menu';

export interface Column {
  key: string;
  label: string;
}

// ExpandedRow marks the template shown under a row when it is clicked, its context is the row resource
@Directive({ selector: 'ng-template[flockExpandedRow]' })
export class ExpandedRow {
  readonly template = inject(TemplateRef);
}

const expandMs = 225;
type ExpandState = 'opening' | 'open' | 'closing';

// ResourceTable is the table of every resource page: search by name or uuid, columns to show, checkbox
// selection, ⋮ menu, rows expanded on click and pages of the list. The page gives the cells as matColumnDef.
@Component({
  selector: 'flock-resource-table',
  imports: [
    FormsModule,
    NgTemplateOutlet,
    MatTableModule,
    MatPaginatorModule,
    MatFormFieldModule,
    MatInputModule,
    MatSelectModule,
    MatIconModule,
    MatButtonModule,
    MatCheckboxModule,
    MatTooltipModule,
    ResourceMenu,
    ProvisionedBy,
    MetadataChips,
  ],
  templateUrl: './resource-table.html',
  styleUrl: './resource-table.scss',
})
export class ResourceTable implements AfterContentInit {
  readonly columns = input.required<Column[]>();
  // keys of the columns hidden until the user shows them
  readonly hiddenColumns = input<string[]>(['uuid']);
  // a new load function lists something else: the table goes back to the first page
  readonly load = input.required<(page: ListPage) => Promise<Resource[]>>();
  readonly selectable = input(true);
  readonly searchable = input(true);
  readonly pageSizes = input([1, 5, 10, 25]);
  // items added to the ⋮ menu of every row, given the row
  readonly extraItems = input<(row: Resource) => MenuItem[]>(() => []);
  readonly showMetadata = input(true);

  @ViewChild(MatTable, { static: true }) private table!: MatTable<Resource>;
  @ContentChildren(MatColumnDef) private columnDefs!: QueryList<MatColumnDef>;
  @ContentChild(ExpandedRow) protected expandedRow?: ExpandedRow;

  protected rows = signal<Resource[]>([]);
  protected search = '';
  private appliedSearch = signal('');
  // bumped by reload() so that the fetch effect runs again for the same page
  private reloads = signal(0);
  // the search the last list was made for: listing it again (a refresh) keeps the rows as the user left them
  private listedSearch = '';
  protected pageIndex = signal(0);
  protected pageSize = signal(10);
  // the api gives no total: a full page means there may be a next one
  protected length = signal(0);
  protected shownKeys = signal<string[]>([]);
  protected expanded = signal(new Map<string, ExpandState>());
  protected selection = new SelectionModel<Resource>(true, []);
  private router = inject(Router);
  protected readonly provisionerUuidOf = provisionerUuidOf;
  protected metadataOf = (row: Resource) => (row as { metadata?: Record<string, string> }).metadata;

  protected displayedColumns = computed(() => {
    const shown = this.columns()
      .map((column) => column.key)
      .filter((key) => this.shownKeys().includes(key));
    const leading = this.selectable() ? ['select'] : [];
    const trailing = this.expandedRow ? ['actions', 'expand'] : ['actions'];
    return [...leading, ...shown, ...trailing];
  });

  constructor() {
    effect(() => {
      const hidden = this.hiddenColumns();
      this.shownKeys.set(
        this.columns()
          .map((column) => column.key)
          .filter((key) => !hidden.includes(key)),
      );
    });

    inject(ActivatedRoute)
      .queryParamMap.pipe(takeUntilDestroyed(inject(DestroyRef)))
      .subscribe((params) => {
        const search = params.get('uuid') ?? params.get('name');
        if (search !== null) {
          this.search = search;
          this.appliedSearch.set(search);
          this.pageIndex.set(0);
        }
      });

    effect(() => {
      this.load();
      untracked(() => this.pageIndex.set(0));
    });

    effect(() => {
      const load = this.load();
      const limit = this.pageSize();
      const offset = this.pageIndex() * limit;
      const search = this.appliedSearch();
      this.reloads();
      untracked(() => this.fetch(load, limit, offset, search));
    });
  }

  ngAfterContentInit() {
    this.columnDefs.forEach((columnDef) => this.table.addColumnDef(columnDef));
  }

  // Lists the current page again, after a save or a delete.
  reload() {
    this.reloads.update((count) => count + 1);
  }

  protected applySearch() {
    this.pageIndex.set(0);
    this.appliedSearch.set(this.search);
    // the search goes in the url, as the links between resources give it, so that the page can be shared
    const searchesUuid = isUuid(this.search);
    this.router.navigate([], {
      queryParams: {
        uuid: searchesUuid ? this.search : null,
        name: this.search !== '' && !searchesUuid ? this.search : null,
      },
      queryParamsHandling: 'merge',
      replaceUrl: true,
    });
  }

  protected clearSearch() {
    this.search = '';
    this.applySearch();
  }

  protected onPage(event: PageEvent) {
    this.pageSize.set(event.pageSize);
    this.pageIndex.set(event.pageIndex);
  }

  protected trackById = (_index: number, row: Resource) => resourceId(row);

  protected expandState(row: Resource): ExpandState | undefined {
    return this.expanded().get(resourceId(row));
  }

  // rowActions are the main items of the ⋮ menu (Edit, Delete), repeated as buttons in the expanded row where the
  // user is already looking at the resource
  protected rowActions(row: Resource): MenuItem[] {
    return this.extraItems()(row).filter((item) => item.tone !== undefined);
  }

  // disabledInteractive keeps a greyed button clickable for its tooltip, so the click itself must be ignored
  protected runAction(item: MenuItem) {
    if (item.disabled) {
      return;
    }
    item.run();
  }

  protected toggleExpanded(row: Resource) {
    if (!this.expandedRow) {
      return;
    }
    const id = resourceId(row);
    if (this.expandState(row)) {
      this.setExpandState(id, 'closing');
      setTimeout(() => this.setExpandState(id, undefined), expandMs);
      return;
    }
    // rendered collapsed first, the height transition starts on the next frame
    this.setExpandState(id, 'opening');
    requestAnimationFrame(() => this.setExpandState(id, 'open'));
  }

  protected isAllSelected(): boolean {
    return this.selection.selected.length === this.rows().length;
  }

  protected toggleAllRows() {
    if (this.isAllSelected()) {
      this.selection.clear();
      return;
    }
    this.selection.select(...this.rows());
  }

  private setExpandState(id: string, state: ExpandState | undefined) {
    this.expanded.update((expanded) => {
      const next = new Map(expanded);
      if (state) {
        next.set(id, state);
      } else {
        next.delete(id);
      }
      return next;
    });
  }

  private async fetch(
    load: (page: ListPage) => Promise<Resource[]>,
    limit: number,
    offset: number,
    search: string,
  ) {
    const filters = search === '' ? [] : [nameOrUuidFilter(search)];
    const rows = await load({ limit, offset, filters });
    this.rows.set(rows);
    this.selection.clear();
    const mayHaveNextPage = rows.length === limit;
    this.length.set(offset + rows.length + (mayHaveNextPage ? limit : 0));
    const isNewSearch = search !== this.listedSearch;
    this.listedSearch = search;
    // a search finding a single resource is looking at that resource: its details are shown right away
    const foundOne = search !== '' && rows.length === 1;
    if (foundOne && isNewSearch && this.expandedRow) {
      this.setExpandState(resourceId(rows[0]), 'open');
    }
  }
}
