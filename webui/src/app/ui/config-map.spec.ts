import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import ELK from 'elkjs/lib/elk.bundled.js';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api } from '../core/api';
import { InstanceHealth } from '../core/instance-health';
import { ConfigMap, loadBackends, revealShift } from './config-map';

describe('loadBackends', () => {
  it('reads every backend, a backend not found is null', async () => {
    const shop = create(BackendSchema, { UUID: 'c1', name: 'shop' });
    const readBackend = vi.fn(async ({ UUID }: { UUID: string }) => {
      if (UUID === 'c1') {
        return { backend: shop };
      }
      throw new ConnectError('not found', Code.NotFound);
    });
    const api = { backend: { readBackend } } as never;

    const backends = await loadBackends(api, ['c1', 'c2']);

    expect(backends.get('c1')).toBe(shop);
    expect(backends.get('c2')).toBeNull();
    expect(readBackend).toHaveBeenCalledTimes(2);
  });

  it('rejects on any other error', async () => {
    const readBackend = vi.fn(async () => {
      throw new ConnectError('unavailable', Code.Unavailable);
    });
    const api = { backend: { readBackend } } as never;

    await expect(loadBackends(api, ['c1'])).rejects.toThrow();
  });
});

@Component({
  imports: [ConfigMap],
  template: `<flock-config-map vsUuid="s1" />`,
})
class Host {}

describe('revealShift', () => {
  const map = { left: 0, top: 0, right: 800, bottom: 600 };

  it('does not move a summary already inside the map', () => {
    const summary = { left: 100, top: 100, right: 400, bottom: 300 };

    expect(revealShift(summary, map, 16)).toEqual({ x: 0, y: 0 });
  });

  it('moves a summary cut at the bottom or the right back inside', () => {
    const summary = { left: 600, top: 500, right: 900, bottom: 700 };

    expect(revealShift(summary, map, 16)).toEqual({ x: -116, y: -116 });
  });

  it('moves a summary cut at the top or the left back inside', () => {
    const summary = { left: -50, top: -20, right: 250, bottom: 180 };

    expect(revealShift(summary, map, 16)).toEqual({ x: 66, y: 36 });
  });

  it('keeps the top of a summary taller than the map in view', () => {
    const summary = { left: 100, top: 300, right: 400, bottom: 1100 };

    expect(revealShift(summary, map, 16)).toEqual({ x: 0, y: -284 });
  });
});

describe('ConfigMap', () => {
  it('tells when the virtual server is gone', async () => {
    TestBed.configureTestingModule({
      providers: [
        {
          provide: InstanceHealth,
          useValue: { vss: signal([]), endpointStats: signal(new Map()), vipsOf: () => [] },
        },
        { provide: Api, useValue: { backend: { readBackend: vi.fn() } } },
      ],
    });
    const fixture = TestBed.createComponent(Host);
    await fixture.whenStable();
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).toContain('This virtual server no longer exists.');
    expect(fixture.nativeElement.querySelector('vflow')).toBeNull();
  });

  it('tells when the map could not be laid out', async () => {
    vi.spyOn(ELK.prototype, 'layout').mockRejectedValue(new Error('elk failed'));
    const vs = create(VirtualServerSchema, {
      UUID: 's1',
      name: 'shop',
      port: 443,
      routeConfig: { case: 'tcpRouteConfig', value: { backendUuid: 'c1' } },
    } as never);
    const readBackend = vi.fn(async () => ({
      backend: create(BackendSchema, { UUID: 'c1', name: 'api' }),
    }));
    TestBed.configureTestingModule({
      providers: [
        {
          provide: InstanceHealth,
          useValue: { vss: signal([vs]), endpointStats: signal(new Map()), vipsOf: () => [] },
        },
        { provide: Api, useValue: { backend: { readBackend } } },
      ],
    });
    const fixture = TestBed.createComponent(Host);
    await fixture.whenStable();
    await vi.waitFor(() => {
      fixture.detectChanges();
      expect(fixture.nativeElement.textContent).toContain('The map could not be laid out.');
    });

    expect(fixture.nativeElement.textContent).not.toContain('Loading the configuration ...');
    vi.restoreAllMocks();
  });
});
