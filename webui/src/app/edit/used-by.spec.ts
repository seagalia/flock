import { create } from '@bufbuild/protobuf';
import { BackendSchema } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { VirtualServerSchema } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { Api } from '../core/api';
import { linkedToFilter } from '../core/resources';
import { usedBy } from './used-by';

describe('usedBy', () => {
  it('lists the virtual servers returned for the filter of a backend', async () => {
    const backend = create(BackendSchema, { UUID: 'b1', name: 'web' });
    const vs = create(VirtualServerSchema, { UUID: 'v1', name: 'web-https' });
    const list = vi.fn(async () => [vs]);

    const found = await usedBy({ list } as unknown as Api, backend);

    expect(found).toEqual([{ kind: 'vs', resource: vs }]);
    expect(list).toHaveBeenCalledWith('vs', {
      limit: 100,
      offset: 0,
      filters: [linkedToFilter(backend)],
    });
  });

  it('asks nothing for an instance', async () => {
    const list = vi.fn();
    const instance = { $typeName: 'flock.api.config.instance.v1.Instance', name: 'i', UUID: 'i1' };

    expect(await usedBy({ list } as unknown as Api, instance as never)).toEqual([]);
    expect(list).not.toHaveBeenCalled();
  });
});
