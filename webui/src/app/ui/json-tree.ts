import { Component, input, signal } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';

interface Entry {
  key: string;
  value: unknown;
}

// JsonTree shows a json value with collapsible objects and arrays, the "Json Viewer" of the raw details
@Component({
  selector: 'flock-json-tree',
  imports: [MatIconModule],
  template: `
    @if (isContainer(value())) {
      @for (entry of entries(value()); track entry.key) {
        <div class="entry">
          @if (isContainer(entry.value)) {
            <button class="toggle" type="button" (click)="toggle(entry.key)">
              <mat-icon>{{ isCollapsed(entry.key) ? 'chevron_right' : 'expand_more' }}</mat-icon>
              <span class="key">{{ entry.key }}</span>
              <span class="hint">{{ summary(entry.value) }}</span>
            </button>
            @if (!isCollapsed(entry.key)) {
              <div class="children"><flock-json-tree [value]="entry.value" /></div>
            }
          } @else {
            <span class="leaf-key">{{ entry.key }}:</span>
            <span [class]="'value ' + typeOf(entry.value)">{{ format(entry.value) }}</span>
          }
        </div>
      }
    } @else {
      <span [class]="'value ' + typeOf(value())">{{ format(value()) }}</span>
    }
  `,
  styles: `
    :host {
      display: block;
      font-family: var(--flock-mono);
      font-size: 13px;
    }
    .entry {
      padding: 2px 0;
    }
    .toggle {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      border: none;
      background: none;
      color: inherit;
      font: inherit;
      cursor: pointer;
      padding: 0;
      mat-icon {
        font-size: 18px;
        width: 18px;
        height: 18px;
      }
    }
    .key,
    .leaf-key {
      font-weight: 600;
    }
    .leaf-key {
      margin-left: 22px;
      margin-right: 6px;
    }
    .hint {
      color: var(--mat-sys-on-surface-variant);
    }
    .children {
      margin-left: 18px;
      border-left: 1px solid var(--flock-border);
      padding-left: 6px;
    }
    .value.string {
      color: var(--flock-json-string);
    }
    .value.number,
    .value.boolean {
      color: var(--flock-json-number);
    }
    .value.null {
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class JsonTree {
  readonly value = input.required<unknown>();
  private collapsed = signal(new Set<string>());

  protected isContainer(value: unknown): value is object {
    return typeof value === 'object' && value !== null;
  }

  protected entries(value: unknown): Entry[] {
    if (Array.isArray(value)) {
      return value.map((item, index) => ({ key: String(index), value: item }));
    }
    return Object.entries(value as object).map(([key, item]) => ({ key, value: item }));
  }

  protected summary(value: unknown): string {
    if (Array.isArray(value)) {
      return `Array[${value.length}]`;
    }
    return `Object{${Object.keys(value as object).length}}`;
  }

  protected typeOf(value: unknown): string {
    return value === null ? 'null' : typeof value;
  }

  protected format(value: unknown): string {
    if (typeof value === 'string') {
      return `"${value}"`;
    }
    return String(value);
  }

  protected isCollapsed(key: string): boolean {
    return this.collapsed().has(key);
  }

  protected toggle(key: string) {
    this.collapsed.update((collapsed) => {
      const next = new Set(collapsed);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }
}
