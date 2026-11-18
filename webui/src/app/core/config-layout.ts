import type { ELK as Elk } from 'elkjs/lib/elk-api';
import { columnOf, ConfigGraph, NodeKind } from './config-graph';

export const nodeWidth = 184;
export const nodeHeight = 56;
export const headHeight = 20;
const headGap = 12;

export interface Point {
  x: number;
  y: number;
}

// elk is about 1.5 MB, imported on the first layout so the Overview shows before it is loaded. The bundled build runs
// in the page thread, there is no worker file to serve.
let elk: Promise<Elk> | undefined;

function loadElk(): Promise<Elk> {
  elk ??= import('elkjs/lib/elk.bundled.js').then((module) => new module.default());
  return elk;
}

// layoutConfigGraph pins each kind of node to its own column (elk partitions) so the map always reads
// vips → virtual server → routes → backends → endpoints, and lets elk order the rows to limit crossings.
export async function layoutConfigGraph(graph: ConfigGraph): Promise<Map<string, Point>> {
  const laidOut = await (
    await loadElk()
  ).layout({
    id: 'root',
    layoutOptions: {
      'elk.algorithm': 'layered',
      'elk.direction': 'RIGHT',
      'elk.partitioning.activate': 'true',
      'elk.spacing.nodeNode': '24',
      'elk.layered.spacing.nodeNodeBetweenLayers': '72',
    },
    children: graph.nodes.map((node) => ({
      id: node.id,
      width: nodeWidth,
      height: nodeHeight,
      layoutOptions: { 'elk.partitioning.partition': String(columnOf[node.kind]) },
    })),
    edges: graph.edges.map((edge) => ({
      id: edge.id,
      sources: [edge.source],
      targets: [edge.target],
    })),
  });

  const positions = new Map<string, Point>();
  for (const child of laidOut.children ?? []) {
    positions.set(child.id, { x: child.x ?? 0, y: child.y ?? 0 });
  }
  return positions;
}

// columnHeads places the name of each column above its top card. They share one row, above the highest card of
// the map, so they read as a table header rather than as one more card.
export function columnHeads(
  graph: ConfigGraph,
  positions: Map<string, Point>,
): Map<NodeKind, Point> {
  const ys = [...positions.values()].map((point) => point.y);
  const y = Math.min(...ys) - headGap - headHeight;
  const heads = new Map<NodeKind, Point>();
  for (const node of graph.nodes) {
    const point = positions.get(node.id);
    if (point && !heads.has(node.kind)) {
      heads.set(node.kind, { x: point.x, y });
    }
  }
  return heads;
}

// structureKey changes only when a node or an edge is added or removed, so the caller can keep the
// positions (and not move the map under the user's eyes) while health changes.
export function structureKey(graph: ConfigGraph): string {
  const nodeIds = graph.nodes.map((node) => node.id).sort();
  const edgeIds = graph.edges.map((edge) => edge.id).sort();
  return [...nodeIds, '|', ...edgeIds].join(',');
}
