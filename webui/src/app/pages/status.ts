import { Component, effect, inject, signal } from '@angular/core';
import { DescMessage, Message } from '@bufbuild/protobuf';
import { anyUnpack } from '@bufbuild/protobuf/wkt';
import { MatButtonModule } from '@angular/material/button';
import { MatDividerModule } from '@angular/material/divider';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatSelectModule } from '@angular/material/select';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { MtxPopoverModule } from '@ng-matero/extensions/popover';
import { diff, atomizeChangeset, IAtomicChange, Operation } from 'json-diff-ts';
import { Agent } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { BackendEndpointStats } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { StatusResponse, StatusResponse_State } from '../../gen/flock/services/lookup/v1/lookup_pb';
import { Note, Note_Level } from '../../gen/flock/type/v1/note_pb';
import { Api } from '../core/api';
import { Instances } from '../core/instances';
import {
  Kind,
  KindName,
  kinds,
  messageToJson,
  Resource,
  resourceId,
  typeUrl,
} from '../core/resources';
import { BackendStats } from '../ui/backend-stats';

interface StatusRow {
  id: string;
  name: string;
  state: StatusResponse_State;
  error: string;
  notes: Note[];
  // endpoint statuses of a backend, set for the endpoint kind
  endpointStats: BackendEndpointStats | null;
  diff: IAtomicChange[];
}

const statusKinds: KindName[] = ['endpoint', 'vs', 'backend', 'certificate', 'vip', 'instance'];
const anyAgent = '';

// Status shows what the agents of an instance did with each resource: applied, errored or unknown
@Component({
  selector: 'flock-status',
  imports: [
    MatTableModule,
    MatDividerModule,
    MatFormFieldModule,
    MatSelectModule,
    MatIconModule,
    MatButtonModule,
    MtxPopoverModule,
    MatTooltipModule,
    RouterLink,
    BackendStats,
  ],
  templateUrl: './status.html',
  styleUrl: './status.scss',
})
export class Status {
  protected instances = inject(Instances);
  protected readonly kindChoices: Kind[] = statusKinds.map((name) => kinds[name]);
  protected kind = signal<Kind>(kinds.endpoint);
  // ?agent= comes from the agent panel, which opens this page on one agent
  protected agentUuid = signal(
    inject(ActivatedRoute).snapshot.queryParamMap.get('agent') ?? anyAgent,
  );
  protected rows = signal<StatusRow[]>([]);
  protected expanded = signal(new Set<string>());
  protected shownDiff = signal<IAtomicChange[]>([]);
  protected readonly anyAgent = anyAgent;
  protected readonly State = StatusResponse_State;
  protected readonly Operation = Operation;
  protected readonly endpointColumns = ['backend_uuid', 'state', 'error', 'expand'];
  protected readonly resourceColumns = ['name', 'state', 'error', 'notes'];
  protected readonly Note_Level = Note_Level;
  protected readonly noteLabel = noteLabel;
  private api = inject(Api);

  constructor() {
    this.instances.load(inject(ActivatedRoute).snapshot.queryParamMap.get('instance'));
    effect(() => {
      const instance = this.instances.current();
      const kind = this.kind();
      const agentUuid = this.agentUuid();
      if (!instance) {
        return;
      }
      this.rows.set([]);
      this.expanded.set(new Set());
      this.api.lookup
        .listStatus({ instanceUuid: instance.UUID, agentUuid, typeUrl: typeUrl(kind) })
        .then((resp) => this.rows.set(resp.status.map((status) => toRow(kind, status))));
    });
  }

  protected agentLabel(agent: Agent): string {
    return agent.name ? `${agent.name} (${agent.UUID})` : agent.UUID;
  }

  protected isExpanded(row: StatusRow): boolean {
    return this.expanded().has(row.id);
  }

  protected toggle(row: StatusRow) {
    this.expanded.update((expanded) => {
      const next = new Set(expanded);
      if (next.has(row.id)) {
        next.delete(row.id);
      } else {
        next.add(row.id);
      }
      return next;
    });
  }
}

function unpack(
  status: StatusResponse,
  field: 'previousResource' | 'erroredResource',
  schema: DescMessage,
) {
  const any = status[field];
  if (!any) {
    return null;
  }
  return anyUnpack(any, schema) ?? null;
}

const dataPathEngines: Record<string, string> = {
  'starling.datapath.ipvs': 'IPVS',
  'starling.datapath.envoy': 'envoy',
};

export function noteLabel(note: Note): string {
  const engine = dataPathEngines[note.code];
  if (engine) {
    return `${engine} ${note.scope}`;
  }
  return note.message;
}

function toRow(kind: Kind, status: StatusResponse): StatusRow {
  const previous = unpack(status, 'previousResource', kind.schema);
  const errored = unpack(status, 'erroredResource', kind.schema);
  const shown = previous ?? errored;
  const endpointStats = kind.name === 'endpoint' ? (shown as BackendEndpointStats | null) : null;
  return {
    id: endpointStats ? endpointStats.backendUuid : shown ? resourceId(shown as Resource) : '',
    name: (shown as Resource | null)?.name ?? '',
    state: status.state,
    error: status.error,
    notes: status.notes,
    endpointStats,
    diff: changes(previous, errored),
  };
}

// changes tells what the agents refused in a resource: from the last applied version to the errored one
function changes(previous: Message | null, errored: Message | null): IAtomicChange[] {
  if (!errored) {
    return [];
  }
  const before = previous ? messageToJson(previous) : {};
  const after = messageToJson(errored);
  return atomizeChangeset(diff(before as object, after as object));
}
