import { isLocked } from './lock';

describe('lock', () => {
  it('locks only on the exact value true', () => {
    expect(isLocked({ metadata: { 'flock.autoprovision': 'true' } })).toBe(true);
    expect(isLocked({ metadata: { 'flock.autoprovision': 'True' } })).toBe(false);
    expect(isLocked({ metadata: { 'flock.autoprovision': 'false' } })).toBe(false);
    expect(isLocked({ metadata: { other: 'true' } })).toBe(false);
    expect(isLocked({})).toBe(false);
  });
});
