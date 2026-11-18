import { inject, Injectable, signal } from '@angular/core';
import { Router } from '@angular/router';
import { Agent } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { Instance } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { Api } from './api';
import { listAll } from './resources';

// Instances keeps the instance chosen on one page (overview, status, monitors) for the others
@Injectable({ providedIn: 'root' })
export class Instances {
  private api = inject(Api);
  private router = inject(Router);
  private loading: Promise<Instance[]> | null = null;

  readonly all = signal<Instance[]>([]);
  readonly current = signal<Instance | null>(null);
  readonly agents = signal<Agent[]>([]);

  // load lists the instances once and selects the first one, the pages wait on it before their own calls.
  // preferredName comes from the ?instance= of a shared link and wins over the instance already selected.
  load(preferredName: string | null = null): Promise<Instance[]> {
    if (!this.loading) {
      this.loading = listAll((limit, offset) =>
        this.api.instance.listInstance({ limit, offset }).then((resp) => resp.instances),
      ).then((instances) => {
        this.all.set(instances);
        if (!this.current() && instances.length > 0) {
          this.select(instances[0]);
        }
        return instances;
      });
      this.loading.catch(() => (this.loading = null));
    }
    return this.loading.then((instances) => {
      const preferred = instances.find((instance) => instance.name === preferredName);
      if (preferred && preferred !== this.current()) {
        this.select(preferred);
      }
      return instances;
    });
  }

  select(instance: Instance) {
    this.current.set(instance);
    this.agents.set([]);
    this.api.lookup
      .listAgent({ instanceUuid: instance.UUID })
      .then((resp) => this.agents.set(resp.agents));
  }

  // choose is a selection made by the user, kept in the url so that the page can be shared
  choose(instance: Instance) {
    this.select(instance);
    this.router.navigate([], {
      queryParams: { instance: instance.name },
      queryParamsHandling: 'merge',
      replaceUrl: true,
    });
  }
}
