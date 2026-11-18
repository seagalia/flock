import { create } from '@bufbuild/protobuf';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { fieldChanges } from './confirm-change';

describe('fieldChanges', () => {
  it('lists the changed, added and removed leaves', () => {
    const before = create(BackendSchema, { name: 'web', sni: 'a', metadata: { team: 'lb' } });
    const after = create(BackendSchema, {
      name: 'api',
      metadata: { team: 'lb', 'flock.autoprovision': 'true' },
    });

    expect(fieldChanges(BackendSchema, before, after)).toEqual([
      { path: 'name', before: '"web"', after: '"api"' },
      { path: 'sni', before: '"a"', after: undefined },
      { path: 'metadata["flock.autoprovision"]', before: undefined, after: '"true"' },
    ]);
  });

  it('finds nothing between equal resources', () => {
    const backend = create(BackendSchema, { name: 'web' });
    expect(fieldChanges(BackendSchema, backend, create(BackendSchema, { name: 'web' }))).toEqual(
      [],
    );
  });
});
