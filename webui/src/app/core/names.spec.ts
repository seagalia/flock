import { isProposedName, proposeName } from './names';

describe('proposeName', () => {
  it('gives a bird and a town in kebab case, which it then recognizes', () => {
    for (let i = 0; i < 50; i++) {
      const name = proposeName();
      expect(name).toMatch(/^[a-z]+(-[a-z]+)+$/);
      expect(isProposedName(name)).toBe(true);
    }
  });

  it('does not recognize a name the user typed or changed', () => {
    expect(isProposedName('web')).toBe(false);
    expect(isProposedName('hoopoe-cassis2')).toBe(false);
    expect(isProposedName('hoopoe-cassis')).toBe(true);
  });
});
