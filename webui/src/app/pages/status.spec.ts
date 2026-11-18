import { create } from '@bufbuild/protobuf';
import { NoteSchema } from '../../gen/flock/type/v1/note_pb';
import { noteLabel } from './status';

describe('noteLabel', () => {
  it('names IPVS for the ipvs data path', () => {
    const note = create(NoteSchema, { code: 'starling.datapath.ipvs', scope: '10.0.0.1:80/tcp' });
    expect(noteLabel(note)).toBe('IPVS 10.0.0.1:80/tcp');
  });

  it('names envoy for the envoy data path', () => {
    const note = create(NoteSchema, { code: 'starling.datapath.envoy', scope: '10.0.0.1:53/udp' });
    expect(noteLabel(note)).toBe('envoy 10.0.0.1:53/udp');
  });

  it('gives the message of other notes', () => {
    const note = create(NoteSchema, { code: 'f5.approximation', message: 'maglev not supported' });
    expect(noteLabel(note)).toBe('maglev not supported');
  });
});
