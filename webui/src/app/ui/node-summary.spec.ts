import { signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import {
  Backend,
  BackendEndpointStatsSchema,
  BackendSchema,
} from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { buildConfigGraph, GraphNode } from '../core/config-graph';
import { InstanceHealth } from '../core/instance-health';
import { NodeSummary } from './node-summary';

const tcpRoute = { case: 'tcpRouteConfig', value: { backendUuid: 'c1' } };
const httpRoute = {
  case: 'httpRoutesConfig',
  value: {
    virtualHosts: [
      {
        domains: ['shop.example.com'],
        routes: [
          {
            match: { pathSpecifier: { case: 'prefix', value: '/api' } },
            action: { case: 'backendUuid', value: 'c1' },
          },
        ],
      },
    ],
  },
};

// a virtual server (tcp unless told) to backend c1, whose endpoints are given as [ip, healthy, lastError]
function graphNode(
  nodeId: string,
  endpoints: [string, boolean, string?][],
  routeConfig: unknown = tcpRoute,
): GraphNode {
  const vs = create(VirtualServerSchema, {
    UUID: 's1',
    name: 'shop',
    port: 443,
    routeConfig,
  } as never);
  const stats = create(BackendEndpointStatsSchema, {
    backendUuid: 'c1',
    endpointStats: endpoints.map(([ip, healthy, lastError]) => ({
      ipAddr: { ip, port: 80 },
      healthy,
      lastError,
    })),
  });
  const endpointStats = new Map([['c1', stats]]);
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      {
        provide: InstanceHealth,
        useValue: {
          endpointStats: signal(endpointStats),
          health: () => null,
          vipsOf: () => [create(VIPSchema, { UUID: 'v1', ip: '10.0.0.9' })],
        },
      },
    ],
  });
  const graph = buildConfigGraph({
    vs,
    vips: [],
    backends: new Map<string, Backend | null>([
      ['c1', create(BackendSchema, { UUID: 'c1', name: 'api' })],
    ]),
    endpointStats,
  });
  return graph.nodes.find((node) => node.id === nodeId) as GraphNode;
}

function buttons(element: HTMLElement, text: string): HTMLElement[] {
  return [...element.querySelectorAll<HTMLElement>('button')].filter((button) =>
    button.textContent?.includes(text),
  );
}

describe('NodeSummary', () => {
  it('offers the test commands of a virtual server and of its routes through each vip', async () => {
    const cases: [string, unknown][] = [
      ['vs:s1', tcpRoute],
      ['route:0', tcpRoute],
      ['route:0', httpRoute],
    ];
    for (const [nodeId, routeConfig] of cases) {
      const node = graphNode(nodeId, [['10.0.0.1', true]], routeConfig);
      const fixture = TestBed.createComponent(NodeSummary);
      fixture.componentRef.setInput('node', node);
      await fixture.whenStable();

      const element: HTMLElement = fixture.nativeElement;
      expect(element.textContent).toContain('10.0.0.9');
      expect(element.querySelector('[aria-label="Copy a test command"]')).not.toBeNull();
      TestBed.resetTestingModule();
    }
  });

  it('offers no test command for a backend', async () => {
    const node = graphNode('backend:c1', [['10.0.0.1', true]]);
    const fixture = TestBed.createComponent(NodeSummary);
    fixture.componentRef.setInput('node', node);
    await fixture.whenStable();

    expect(fixture.nativeElement.querySelector('[aria-label="Copy a test command"]')).toBeNull();
  });

  it('shows the Edit and Delete the page gives, a greyed one with its reason', async () => {
    const node = graphNode('backend:c1', [['10.0.0.1', true]]);
    const edited: string[] = [];
    const fixture = TestBed.createComponent(NodeSummary);
    fixture.componentRef.setInput('node', node);
    fixture.componentRef.setInput('actions', [
      { label: 'Edit', icon: 'edit', tone: 'primary', run: () => edited.push('edit') },
      {
        label: 'Delete',
        icon: 'delete',
        tone: 'danger',
        disabled: 'Locked',
        run: () => edited.push('delete'),
      },
    ]);
    await fixture.whenStable();

    buttons(fixture.nativeElement, 'Edit')[0].click();
    const remove = buttons(fixture.nativeElement, 'Delete')[0];
    remove.click();
    expect(edited).toEqual(['edit']);
    expect(remove.getAttribute('aria-disabled')).toBe('true');
  });

  it('summarizes a degraded backend', async () => {
    const node = graphNode('backend:c1', [
      ['10.0.0.1', true],
      ['10.0.0.2', false, 'connection refused'],
    ]);
    const fixture = TestBed.createComponent(NodeSummary);
    fixture.componentRef.setInput('node', node);
    await fixture.whenStable();

    const element: HTMLElement = fixture.nativeElement;
    expect(element.textContent).toContain('Backend');
    expect(element.textContent).toContain('1 of 2 up');
    expect(element.textContent).toContain('connection refused');
  });

  it('summarizes an endpoint', async () => {
    const node = graphNode('endpoint:10.0.0.2:80', [
      ['10.0.0.2', false, 'connection refused'],
      ['10.0.0.2', true],
    ]);
    const fixture = TestBed.createComponent(NodeSummary);
    fixture.componentRef.setInput('node', node);
    await fixture.whenStable();

    const element: HTMLElement = fixture.nativeElement;
    expect(element.textContent).toContain('down on 1 of 2 reports');
    const backendLinks = [...element.querySelectorAll('.backends a')].map((a) => a.textContent);
    expect(backendLinks).toEqual(['api']);
  });
});
