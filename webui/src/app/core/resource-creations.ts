import { Injectable, signal } from '@angular/core';
import { KindName, Resource } from './resources';

export interface Creation {
  kind: KindName;
  created: (resource: Resource) => void;
}

// ResourceCreations opens the creation panel of a resource for a form that links it, such as a backend from the
// backend_uuid picker of a route. The shell shows the panels, so the forms don't import the editor whose panels hold
// forms, which would be an import cycle. A creation panel may open another one, they stack.
@Injectable({ providedIn: 'root' })
export class ResourceCreations {
  readonly open = signal<Creation[]>([]);

  start(creation: Creation) {
    this.open.update((creations) => [...creations, creation]);
  }

  end(creation: Creation) {
    this.open.update((creations) => creations.filter((one) => one !== creation));
  }
}
