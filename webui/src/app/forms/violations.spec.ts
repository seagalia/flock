import { Code, ConnectError } from '@connectrpc/connect';
import { ViolationsSchema } from '../../gen/buf/validate/validate_pb';
import { violationsOf } from './violations';

describe('violationsOf', () => {
  it('maps a ConnectError carrying two violations to two FieldErrors', () => {
    const err = new ConnectError('invalid request', Code.InvalidArgument, undefined, [
      {
        desc: ViolationsSchema,
        value: {
          violations: [
            {
              field: {
                elements: [
                  { fieldName: 'routes', subscript: { case: 'index', value: 0n } },
                  { fieldName: 'backend_uuid' },
                ],
              },
              message: 'value length must be at least 1 characters',
            },
            {
              field: {
                elements: [
                  { fieldName: 'metadata', subscript: { case: 'stringKey', value: 'team' } },
                ],
              },
              message: 'value is required',
            },
          ],
        },
      },
    ]);

    expect(violationsOf(err)).toEqual([
      { path: 'routes[0].backend_uuid', message: 'value length must be at least 1 characters' },
      { path: 'metadata["team"]', message: 'value is required' },
    ]);
  });

  it('gives nothing for another code', () => {
    expect(violationsOf(new ConnectError('denied', Code.PermissionDenied))).toEqual([]);
  });
});
