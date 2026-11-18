import { Component, computed, input, output } from '@angular/core';
import { Message } from '@bufbuild/protobuf';
import { MatTabsModule } from '@angular/material/tabs';
import { kindOf, messageToJson } from '../core/resources';
import { JsonTree } from './json-tree';
import { SidePanel } from './side-panel';

// RawDetails is the side panel of a resource as raw json, opened from the ⋮ menu or from a summary card.
// The parent renders it to open it and removes it on (closed).
@Component({
  selector: 'flock-raw-details',
  imports: [MatTabsModule, SidePanel, JsonTree],
  template: `
    <flock-side-panel
      [title]="'Raw Details for ' + kindLabel()"
      [subject]="name()"
      (closed)="closed.emit()"
    >
      <mat-tab-group dynamicHeight>
        <mat-tab label="Json Viewer">
          <div class="raw"><flock-json-tree [value]="json()" /></div>
        </mat-tab>
        <mat-tab label="Plain">
          <pre class="raw plain">{{ plain() }}</pre>
        </mat-tab>
      </mat-tab-group>
    </flock-side-panel>
  `,
  styles: `
    .raw {
      border: 1px solid var(--flock-border);
      border-radius: 8px;
      padding: 15px;
      margin-top: 10px;
    }
    .plain {
      font-family: var(--flock-mono);
      font-size: 13px;
      max-height: 65vh;
      overflow: auto;
    }
  `,
})
export class RawDetails {
  readonly resource = input.required<Message>();
  readonly closed = output<void>();

  // the messages which are not resources, like the permissions, are labelled by their type name
  protected kindLabel = computed(() => {
    try {
      return kindOf(this.resource()).label;
    } catch {
      return this.resource().$typeName.split('.').pop() ?? '';
    }
  });
  protected name = computed(() => {
    const resource = this.resource() as Message & {
      name?: string;
      member?: { memberName: string };
    };
    return resource.name ?? resource.member?.memberName ?? '';
  });
  protected json = computed(() => messageToJson(this.resource()));
  protected plain = computed(() => JSON.stringify(this.json(), null, 2));
}
