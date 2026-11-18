import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { Api } from '../core/api';
import { LinksPermissions } from './links-permissions';

@Component({
  imports: [LinksPermissions],
  template: `<flock-links-permissions [resource]="resource" [linkedKinds]="[]" />`,
})
class Host {
  resource = create(BackendSchema, { UUID: 'b1', name: 'web' });
}

async function renderWith(allowAllRead: boolean): Promise<string> {
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      {
        provide: Api,
        useValue: {
          permission: {
            listMemberPermissions: async () => ({ memberPermissions: [], allowAllRead }),
          },
          list: async () => [],
        },
      },
    ],
  });
  const fixture = TestBed.createComponent(Host);
  await fixture.whenStable();
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
  return fixture.nativeElement.querySelector('.allow-all-read').textContent;
}

describe('LinksPermissions', () => {
  it('says every user can read when allow-all-read is on', async () => {
    expect(await renderWith(true)).toContain('Every user can read');
  });

  it('says only the members can read when allow-all-read is off', async () => {
    expect(await renderWith(false)).toContain('Only the members below can read');
  });
});
