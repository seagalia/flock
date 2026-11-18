import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { create } from '@bufbuild/protobuf';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { MenuItem, ResourceMenu } from './resource-menu';

@Component({
  imports: [ResourceMenu],
  template: `<flock-resource-menu [resource]="vip" [extraItems]="items" />`,
})
class Host {
  vip = create(VIPSchema, { UUID: 'uuid-0', name: 'vip-0' });
  enabledRun = vi.fn();
  disabledRun = vi.fn();
  items: MenuItem[] = [
    { label: 'Edit', icon: 'edit', run: this.enabledRun },
    { label: 'Delete', icon: 'delete', disabled: 'You need the owner role', run: this.disabledRun },
  ];
}

describe('ResourceMenu', () => {
  async function openMenu() {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    (fixture.nativeElement.querySelector('button[aria-label="Actions"]') as HTMLElement).click();
    fixture.detectChanges();
    await fixture.whenStable();
    return fixture;
  }

  function item(label: string): HTMLElement {
    const items = Array.from(document.querySelectorAll<HTMLElement>('button[mat-menu-item]'));
    return items.find((button) => button.textContent?.includes(label))!;
  }

  it('runs an enabled extra item', async () => {
    const fixture = await openMenu();

    item('Edit').click();

    expect(fixture.componentInstance.enabledRun).toHaveBeenCalledTimes(1);
  });

  it('never runs a disabled extra item', async () => {
    const fixture = await openMenu();

    item('Delete').click();

    expect(fixture.componentInstance.disabledRun).not.toHaveBeenCalled();
  });
});
