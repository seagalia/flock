import {
  Component,
  computed,
  DestroyRef,
  DOCUMENT,
  effect,
  ElementRef,
  inject,
  input,
  linkedSignal,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { Code, ConnectError } from '@connectrpc/connect';
import { Edge, HtmlTemplateNode, Vflow, VflowComponent } from 'ngx-vflow';
import { Backend } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VIP } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServer } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api, silentCall } from '../core/api';
import { buildConfigGraph, ConfigGraph, NodeKind, pathsThrough, recap } from '../core/config-graph';
import {
  columnHeads,
  headHeight,
  layoutConfigGraph,
  nodeHeight,
  nodeWidth,
  Point,
  structureKey,
} from '../core/config-layout';
import { vsBackendUuids } from '../core/display';
import { InstanceHealth } from '../core/instance-health';
import { NodeSummary } from './node-summary';
import { SidePanel } from './side-panel';
import { MenuItem } from './resource-menu';

// loadBackends reads every backend once; one not found is null (drawn as missing), any other error rejects.
// The map tells itself that the backends could not be loaded, the calls stay silent.
export async function loadBackends(
  api: Pick<Api, 'backend'>,
  uuids: string[],
): Promise<Map<string, Backend | null>> {
  const entries = await Promise.all(
    uuids.map(async (uuid): Promise<[string, Backend | null]> => {
      try {
        const resp = await api.backend.readBackend({ UUID: uuid }, silentCall);
        return [uuid, resp.backend ?? null];
      } catch (err) {
        if (ConnectError.from(err).code !== Code.NotFound) {
          throw err;
        }
        return [uuid, null];
      }
    }),
  );
  return new Map(entries);
}

interface Box {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

// revealShift is how far to pan so that the box (the summary of a card) sits inside the frame (the map), with a
// margin; a box taller or wider than the frame keeps its top and left in view, where it starts to read.
export function revealShift(box: Box, frame: Box, margin: number): { x: number; y: number } {
  return {
    x: shiftInto(box.left, box.right, frame.left + margin, frame.right - margin),
    y: shiftInto(box.top, box.bottom, frame.top + margin, frame.bottom - margin),
  };
}

function shiftInto(start: number, end: number, min: number, max: number): number {
  if (end > max) {
    return Math.max(max - end, min - start);
  }
  if (start < min) {
    return min - start;
  }
  return 0;
}

function backendKey(vs: VirtualServer): string {
  return [...vsBackendUuids(vs)].sort().join(',');
}

const columnLabels: Record<NodeKind, string> = {
  vip: 'VIPs',
  vs: 'Virtual server',
  route: 'Routes',
  backend: 'Backends',
  endpoint: 'Endpoints',
};

interface LoadedBackends {
  key: string;
  backends: Map<string, Backend | null>;
}

interface FlowLayout {
  nodes: HtmlTemplateNode[];
  edges: Edge[];
}

// ConfigMap draws the configuration of one virtual server as a graph read the way a request travels:
// vips → virtual server → routes → backends → endpoints. It reads what InstanceHealth already loads, so it
// follows its refreshes, and only reads the backends of the virtual server.
@Component({
  selector: 'flock-config-map',
  imports: [Vflow, NodeSummary],
  templateUrl: './config-map.html',
  styleUrl: './config-map.scss',
})
export class ConfigMap {
  readonly vsUuid = input.required<string>();
  // the Edit and Delete the page offers on the resource of a node, shown in its summary
  readonly actions = input<(resource: VIP | VirtualServer | Backend) => MenuItem[]>(() => []);

  protected instanceHealth = inject(InstanceHealth);
  private api = inject(Api);
  // the side panel the map is shown in, when it is in one
  private panel = inject(SidePanel, { optional: true });
  private flow = viewChild(VflowComponent);
  private map = viewChild<ElementRef<HTMLElement>>('map');
  protected readonly nodeWidth = nodeWidth;
  protected readonly nodeHeight = nodeHeight;
  protected readonly headHeight = headHeight;
  // the column names are drawn as flow nodes too, so they pan and zoom with the cards under them
  protected readonly headLabels = new Map(
    Object.entries(columnLabels).map(([kind, label]) => [`head:${kind}`, label]),
  );

  protected vs = computed(
    () => this.instanceHealth.vss().find((vs) => vs.UUID === this.vsUuid()) ?? null,
  );
  // a refresh gives a new virtual server, its backends are read again only when their list changes
  private backendKey = computed(() => {
    const vs = this.vs();
    return vs ? backendKey(vs) : null;
  });
  private loaded = signal<LoadedBackends | null>(null);
  protected backendsFailed = signal(false);

  // the graph is built with the backends read for the current list of backends only: while the backends of a
  // changed list are read, or when they could not be, the last graph stays
  protected graph = linkedSignal<
    { vs: VirtualServer | null; loaded: LoadedBackends | null },
    ConfigGraph | null
  >({
    source: () => ({ vs: this.vs(), loaded: this.loaded() }),
    computation: ({ vs, loaded }, previous) => {
      const isCurrent = vs !== null && loaded !== null && loaded.key === backendKey(vs);
      if (!isCurrent) {
        return previous?.value ?? null;
      }
      return buildConfigGraph({
        vs,
        vips: this.instanceHealth.vipsOf(vs),
        backends: loaded.backends,
        endpointStats: this.instanceHealth.endpointStats() ?? new Map(),
      });
    },
  });
  protected recapLine = computed(() => {
    const graph = this.graph();
    return graph ? recap(graph) : '';
  });
  // the cards and wires read their state here: a health refresh restyles them without a new layout
  protected nodeById = computed(() => new Map(this.graph()?.nodes.map((node) => [node.id, node])));
  protected edgeById = computed(() => new Map(this.graph()?.edges.map((edge) => [edge.id, edge])));
  private structure = computed(() => {
    const graph = this.graph();
    return graph ? structureKey(graph) : null;
  });
  protected layout = signal<FlowLayout | null>(null);
  protected layoutFailed = signal(false);

  protected selectedId = signal<string | null>(null);
  // the selection survives a refresh while its node is still on the map
  protected selected = computed(() => {
    const id = this.selectedId();
    return id !== null && this.nodeById().has(id) ? id : null;
  });
  protected highlight = computed(() => {
    const graph = this.graph();
    const selected = this.selected();
    return graph && selected ? pathsThrough(graph, selected) : null;
  });

  constructor() {
    effect(() => {
      const key = this.backendKey();
      if (key === null) {
        return;
      }
      untracked(() => this.readBackends(key));
    });

    effect(() => {
      const key = this.structure();
      if (key === null) {
        return;
      }
      untracked(() => this.lay(key, this.graph() as ConfigGraph));
    });

    // fitted once, when the flow first knows its size (it is drawn only with a layout, so its nodes are set):
    // a later layout keeps the pan and zoom of the user
    let fitted = false;
    effect(() => {
      const flow = this.flow();
      if (fitted || !flow?.initialized()) {
        return;
      }
      fitted = true;
      untracked(() => flow.fitView({ padding: 0.1 }));
    });

    effect(() => {
      const id = this.selected();
      if (id !== null) {
        untracked(() => this.revealSummary(id));
      }
    });

    // Escape first clears the selection; the side panel holding the map sees the event prevented and stays
    const document = inject(DOCUMENT);
    const onKeydown = (event: KeyboardEvent) => {
      // an open menu of the summary (the test commands) closes first, it lives in the overlay container, and so does
      // the panel the Edit of a summary opens over the map
      const inOverlay = (event.target as Element).closest?.('.cdk-overlay-container') != null;
      const isUnderPanel = this.panel !== null && !this.panel.isTop();
      const clearsSelection =
        event.key === 'Escape' && this.selected() !== null && !inOverlay && !isUnderPanel;
      if (!clearsSelection) {
        return;
      }
      event.preventDefault();
      this.selectedId.set(null);
    };
    document.addEventListener('keydown', onKeydown, { capture: true });
    inject(DestroyRef).onDestroy(() =>
      document.removeEventListener('keydown', onKeydown, { capture: true }),
    );
  }

  protected clearOnBackground(event: MouseEvent) {
    const onNode = (event.target as Element).closest('.vflow-node') !== null;
    if (!onNode) {
      this.selectedId.set(null);
    }
  }

  // the summary of a card near the edge of the map would open partly hidden: pan the map to show all of it
  private revealSummary(id: string) {
    // the toolbar first measures the summary, then places it: two frames later it is where it stays
    requestAnimationFrame(() =>
      requestAnimationFrame(() => {
        const flow = this.flow();
        const map = this.map()?.nativeElement;
        const summary = map?.querySelector('flock-node-summary');
        const stillSelected = this.selected() === id;
        if (!flow || !map || !summary || !stillSelected) {
          return;
        }
        const shift = revealShift(summary.getBoundingClientRect(), map.getBoundingClientRect(), 16);
        if (shift.x === 0 && shift.y === 0) {
          return;
        }
        const viewport = flow.viewport();
        flow.panTo({ x: viewport.x + shift.x, y: viewport.y + shift.y });
      }),
    );
  }

  // reloadBackends reads the backends again after the page changed one, a refresh only reads them when their list
  // changes
  reloadBackends() {
    const key = this.backendKey();
    if (key !== null) {
      void this.readBackends(key);
    }
  }

  private async readBackends(key: string) {
    const uuids = key === '' ? [] : key.split(',');
    try {
      const backends = await loadBackends(this.api, uuids);
      // an answer for a list of backends the virtual server no longer has is dropped
      if (this.backendKey() === key) {
        this.loaded.set({ key, backends });
        this.backendsFailed.set(false);
      }
    } catch {
      if (this.backendKey() === key) {
        this.backendsFailed.set(true);
      }
    }
  }

  private async lay(key: string, graph: ConfigGraph) {
    let points: Map<string, Point>;
    try {
      points = await layoutConfigGraph(graph);
    } catch {
      if (this.structure() === key) {
        this.layoutFailed.set(true);
      }
      return;
    }
    // a newer structure is being laid out
    if (this.structure() !== key) {
      return;
    }
    this.layoutFailed.set(false);
    const cards: HtmlTemplateNode[] = graph.nodes.map((node) => ({
      id: node.id,
      type: 'html-template',
      point: signal(points.get(node.id) ?? { x: 0, y: 0 }),
      width: signal(nodeWidth),
      height: signal(nodeHeight),
      draggable: signal(false),
    }));
    const heads: HtmlTemplateNode[] = [...columnHeads(graph, points)].map(([kind, point]) => ({
      id: `head:${kind}`,
      type: 'html-template',
      point: signal(point),
      width: signal(nodeWidth),
      height: signal(headHeight),
      draggable: signal(false),
    }));
    this.layout.set({
      nodes: [...heads, ...cards],
      edges: graph.edges.map((edge) => ({
        id: edge.id,
        source: edge.source,
        target: edge.target,
        type: 'template',
      })),
    });
  }
}
