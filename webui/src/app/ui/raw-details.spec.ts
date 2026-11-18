import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { create } from '@bufbuild/protobuf';
import { VIPSchema } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { RawDetails } from './raw-details';

@Component({
  imports: [RawDetails],
  template: `<flock-raw-details [resource]="vip" />`,
})
class Host {
  readonly vip = create(VIPSchema, { UUID: 'uuid-1', name: 'edge' });
}

describe('RawDetails', () => {
  it('shows the resource as json in a side panel', async () => {
    const fixture = TestBed.createComponent(Host);
    await fixture.whenStable();
    fixture.detectChanges();
    await fixture.whenStable();

    // the panel moves itself to the body
    expect(document.body.textContent).toContain('Raw Details for VIP');
    expect(document.body.textContent).toContain('edge');
    fixture.destroy();
  });
});
