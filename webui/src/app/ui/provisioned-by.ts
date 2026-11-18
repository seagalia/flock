import { Component, effect, inject, input, signal, untracked } from '@angular/core';
import { RouterLink } from '@angular/router';
import { Api, silentCall } from '../core/api';
import { kinds } from '../core/resources';

// provisionByMetadataKey is set by a provisioner on every resource it creates or changes, holding its agent uuid
export const provisionByMetadataKey = 'flock.provision.by';

export function provisionerUuidOf(resource: { metadata?: Record<string, string> }): string {
  return resource.metadata?.[provisionByMetadataKey] ?? '';
}

interface Provisioner {
  name: string;
  instanceUuid: string;
}

// ProvisionedBy names the provisioner of a resource and opens its panel in the instances page. A uuid no declared agent
// has (the provisioner was removed from the config) stays shown raw, with no link.
@Component({
  selector: 'flock-provisioned-by',
  imports: [RouterLink],
  template: `
    <p class="provisioned-by">
      <span class="label">Provisioned by</span>
      @if (provisioner(); as provisioner) {
        <a
          [routerLink]="instanceLink"
          [queryParams]="{ uuid: provisioner.instanceUuid, agent: agentUuid() }"
          >{{ provisioner.name }}</a
        >
      } @else {
        <span class="mono">{{ agentUuid() }}</span>
      }
    </p>
  `,
  styles: `
    .provisioned-by {
      display: flex;
      align-items: baseline;
      gap: 8px;
      margin: 0 0 10px;
    }
    .label {
      color: var(--mat-sys-on-surface-variant);
    }
    .mono {
      font-family: var(--flock-mono);
      font-size: 13px;
    }
  `,
})
export class ProvisionedBy {
  readonly agentUuid = input.required<string>();
  private api = inject(Api);

  protected readonly instanceLink = kinds.instance.link;
  protected provisioner = signal<Provisioner | null>(null);

  constructor() {
    effect(() => {
      const agentUuid = this.agentUuid();
      untracked(() => this.load(agentUuid));
    });
  }

  private async load(agentUuid: string) {
    this.provisioner.set(null);
    try {
      const { agent, instanceUuid } = await this.api.lookup.readAgent({ agentUuid }, silentCall);
      const isCurrent = this.agentUuid() === agentUuid;
      if (agent && isCurrent) {
        this.provisioner.set({ name: agent.name, instanceUuid });
      }
    } catch {
      // NotFound or no reader role on its instance: the uuid stays shown
    }
  }
}
