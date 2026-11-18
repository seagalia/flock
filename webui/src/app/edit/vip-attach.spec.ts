import { create } from '@bufbuild/protobuf';
import { TestBed } from '@angular/core/testing';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef } from '@angular/material/dialog';
import { ConnectError } from '@connectrpc/connect';
import { of } from 'rxjs';
import { VIP, VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api } from '../core/api';
import { ConfirmDialog } from '../ui/confirm-dialog';
import { ErrorDialog } from '../ui/error-dialog';
import { Access } from './access';
import { applyVipChanges, VipAttach, VipAttachDialog, vipChanges } from './vip-attach';

const vs = create(VirtualServerSchema, { UUID: 'v1', name: 'web', port: 443 });
const front = create(VIPSchema, {
  UUID: 'ip1',
  name: 'front',
  ip: '10.0.0.1',
  virtualServersUuid: ['v1'],
});
const edge = create(VIPSchema, {
  UUID: 'ip2',
  name: 'edge',
  ip: '10.0.0.2',
  virtualServersUuid: ['v9'],
});
const locked = create(VIPSchema, {
  UUID: 'ip3',
  name: 'managed',
  ip: '10.0.0.3',
  metadata: { 'flock.autoprovision': 'true' },
});

// fakeApi stores the VIPs by uuid, an update replaces the stored copy
function fakeApi(vips: VIP[], failOn = '') {
  const stored = new Map(vips.map((vip) => [vip.UUID, vip]));
  return {
    stored,
    vip: {
      listVIP: vi.fn(async () => ({ vip: [...stored.values()] })),
      readVIP: vi.fn(async (req: { UUID: string }) => ({ vip: stored.get(req.UUID) })),
      updateVIP: vi.fn(async (req: { UUID: string; vip: VIP }) => {
        if (req.UUID === failOn) {
          throw new ConnectError('could not find virtual servers v1');
        }
        stored.set(req.UUID, req.vip);
        return {};
      }),
    },
  };
}

describe('vipChanges', () => {
  it('lists the VIPs to attach and to detach', () => {
    const changes = vipChanges(vs, [front, edge], new Set(['ip2']));

    expect(changes.map((change) => [change.vip.name, change.attach])).toEqual([
      ['front', false],
      ['edge', true],
    ]);
  });
});

describe('applyVipChanges', () => {
  it('reads each VIP again and keeps the virtual servers added meanwhile', async () => {
    const api = fakeApi([front, edge]);
    api.stored.set('ip2', create(VIPSchema, { ...edge, virtualServersUuid: ['v9', 'v8'] }));

    const outcome = await applyVipChanges(api as unknown as Api, vs, [
      { vip: front, attach: false },
      { vip: edge, attach: true },
    ]);

    expect(outcome.error).toBe('');
    expect(api.stored.get('ip1')!.virtualServersUuid).toEqual([]);
    expect(api.stored.get('ip2')!.virtualServersUuid).toEqual(['v9', 'v8', 'v1']);
  });

  it('stops at the first refusal and tells what is left', async () => {
    const api = fakeApi([front, edge], 'ip2');

    const outcome = await applyVipChanges(api as unknown as Api, vs, [
      { vip: front, attach: false },
      { vip: edge, attach: true },
    ]);

    expect(outcome.done).toEqual(['detach web from the VIP front (10.0.0.1)']);
    expect(outcome.notDone).toEqual(['attach web to the VIP edge (10.0.0.2)']);
    expect(outcome.error).toBe('could not find virtual servers v1');
  });
});

describe('VipAttachDialog', () => {
  function render(creating: boolean, vips = [front, edge, locked]) {
    TestBed.configureTestingModule({
      providers: [
        { provide: MAT_DIALOG_DATA, useValue: { vs, vips, creating } },
        { provide: MatDialogRef, useValue: { close: vi.fn() } },
      ],
    });
    const fixture = TestBed.createComponent(VipAttachDialog);
    fixture.detectChanges();
    return fixture;
  }

  const boxes = (element: HTMLElement) => [
    ...element.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'),
  ];
  const saveButton = (element: HTMLElement) =>
    [...element.querySelectorAll('button')].find((button) => button.textContent!.includes('Save'))!;

  it('checks the VIPs listing the virtual server and greys the locked ones', () => {
    const element = render(false).nativeElement as HTMLElement;

    expect(boxes(element).map((box) => box.checked)).toEqual([true, false, false]);
    expect(boxes(element)[2].disabled).toBe(true);
  });

  it('offers Save only once a box changed', () => {
    const fixture = render(false);
    const element = fixture.nativeElement as HTMLElement;
    expect(saveButton(element).disabled).toBe(true);

    boxes(element)[1].click();
    fixture.detectChanges();

    expect(saveButton(element).disabled).toBe(false);
  });

  it('reads as a proposal right after a Create', () => {
    const element = render(true).nativeElement as HTMLElement;

    expect(element.textContent).toContain('Attach web to VIPs?');
    expect(element.textContent).toContain('Not now');
  });

  // many is a list long enough for the search field, the VIP attached to web sits at the end
  const many = [
    ...Array.from({ length: 11 }, (_, index) =>
      create(VIPSchema, { UUID: `n${index}`, name: `vip-${index}`, ip: `10.1.0.${index}` }),
    ),
    front,
  ];
  const labels = (element: HTMLElement) =>
    [...element.querySelectorAll('mat-checkbox')].map(
      (box) => box.textContent!.trim().split(' ')[0],
    );

  it('lists the attached VIPs first and counts them', () => {
    const element = render(false, many).nativeElement as HTMLElement;

    expect(labels(element)[0]).toBe('front');
    expect(element.querySelector('.count')!.textContent).toBe('Attached to 1 of 12 VIPs.');
  });

  it('filters a long list by name or IP and keeps the checked boxes', () => {
    const fixture = render(false, many);
    const element = fixture.nativeElement as HTMLElement;
    const search = element.querySelector<HTMLInputElement>('input[name="search"]')!;

    search.value = '10.1.0.1';
    search.dispatchEvent(new Event('input'));
    fixture.detectChanges();
    expect(labels(element)).toEqual(['vip-1', 'vip-10']);

    boxes(element)[0].click();
    search.value = 'nothing';
    search.dispatchEvent(new Event('input'));
    fixture.detectChanges();
    expect(element.textContent).toContain('No VIP matches nothing.');
    expect(element.querySelector('.count')!.textContent).toBe('Attached to 2 of 12 VIPs.');
  });

  it('leaves the search field out of a short list', () => {
    const element = render(false).nativeElement as HTMLElement;

    expect(element.querySelector('input[name="search"]')).toBeNull();
  });
});

describe('VipAttach', () => {
  let opened: unknown[];

  function setup(api: ReturnType<typeof fakeApi>, picked: Set<string> | undefined, denied = '') {
    opened = [];
    TestBed.configureTestingModule({
      providers: [
        { provide: Api, useValue: api },
        {
          provide: Access,
          useValue: { check: async () => (denied ? { reason: denied } : null) },
        },
        {
          provide: MatDialog,
          useValue: {
            open: (component: unknown) => {
              opened.push(component);
              const answer = component === VipAttachDialog ? picked : true;
              return { afterClosed: () => of(answer) };
            },
          },
        },
      ],
    });
    return TestBed.inject(VipAttach);
  }

  it('confirms then applies the changes of a row', async () => {
    const api = fakeApi([front, edge]);
    const attach = setup(api, new Set(['ip1', 'ip2']));

    const changed = await attach.edit(vs);

    expect(changed).toBe(true);
    expect(opened).toEqual([VipAttachDialog, ConfirmDialog]);
    expect(api.stored.get('ip2')!.virtualServersUuid).toEqual(['v9', 'v1']);
  });

  it('applies the proposal after a Create without asking again', async () => {
    const api = fakeApi([front, edge]);
    const attach = setup(api, new Set(['ip1', 'ip2']));

    await attach.edit(vs, true);

    expect(opened).toEqual([VipAttachDialog]);
    expect(api.vip.updateVIP).toHaveBeenCalledTimes(1);
  });

  it('changes nothing when the user lacks the writer role on a VIP', async () => {
    const api = fakeApi([front, edge]);
    const attach = setup(api, new Set(['ip1', 'ip2']), 'You are a reader of edge.');

    const changed = await attach.edit(vs);

    expect(changed).toBe(false);
    expect(opened).toEqual([VipAttachDialog, ErrorDialog]);
    expect(api.vip.updateVIP).not.toHaveBeenCalled();
  });

  it('changes nothing when the dialog is dismissed', async () => {
    const api = fakeApi([front, edge]);
    const attach = setup(api, undefined);

    expect(await attach.edit(vs)).toBe(false);
    expect(api.vip.updateVIP).not.toHaveBeenCalled();
  });
});
