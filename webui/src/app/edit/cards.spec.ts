import { KindName, kinds } from '../core/resources';
import { cards } from './cards';

const editable: KindName[] = ['backend', 'certificate', 'secret', 'vip', 'vs', 'instance', 'zone'];

describe('cards', () => {
  for (const kind of editable) {
    it(`places every field of a ${kind} in exactly one card`, () => {
      const schema = kinds[kind].schema;
      const placed = cards[kind].flatMap((card) => card.fields);

      for (const field of schema.fields) {
        const names = [field.name, field.oneof?.name];
        const count = placed.filter((name) => names.includes(name)).length;
        expect(count, `${kind}.${field.name}`).toBe(1);
      }
      const memberNames = schema.members.map((member) => member.name);
      expect(placed.filter((name) => !memberNames.includes(name))).toEqual([]);
    });

    it(`keeps the Any fields of a ${kind} in its Advanced card`, () => {
      const anyFields = kinds[kind].schema.fields
        .filter((field) => field.message?.typeName === 'google.protobuf.Any')
        .map((field) => field.name);
      const advanced = cards[kind].filter((card) => card.advanced).flatMap((card) => card.fields);
      expect(advanced).toEqual(anyFields);
    });
  }
});
