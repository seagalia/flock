import { TestBed } from '@angular/core/testing';
import { CliHost } from './cli-host';

describe('CliHost', () => {
  let host: CliHost;
  let fetchMock: ReturnType<typeof vi.fn>;

  const reply = (status: number, body = '') =>
    new Response(status === 204 ? null : body, { status });

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    host = TestBed.inject(CliHost);
  });

  afterEach(() => vi.unstubAllGlobals());

  it('fetches the info once', async () => {
    fetchMock.mockResolvedValue(reply(200, '{"cwd":"/work"}'));

    expect(await host.info()).toEqual({ cwd: '/work' });
    expect(await host.info()).toEqual({ cwd: '/work' });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('is not under the cli when info is not 200 or unreachable', async () => {
    fetchMock.mockResolvedValue(reply(404));
    expect(await host.info()).toBeNull();

    TestBed.resetTestingModule();
    fetchMock.mockRejectedValue(new TypeError('network'));
    expect(await TestBed.inject(CliHost).info()).toBeNull();
  });

  it('maps 204 to ok and sends overwrite', async () => {
    fetchMock.mockResolvedValue(reply(204));

    expect(await host.writeFile('a.yml', 'body', true)).toEqual({ ok: true });
    expect(fetchMock.mock.calls[0][0]).toBe('/cli/v1/file?path=a.yml&overwrite=true');
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: 'PUT', body: 'body' });
  });

  it('maps 409 to exists', async () => {
    fetchMock.mockResolvedValue(reply(409, '{"error":"file exists"}'));

    expect(await host.writeFile('a.yml', 'body', false)).toEqual({ ok: false, exists: true });
  });

  it('maps 422 to problems', async () => {
    const problems = [{ file: 'a.yml', index: 1, field: 'name', message: 'required' }];
    fetchMock.mockResolvedValue(reply(422, JSON.stringify({ problems })));

    expect(await host.writeFile('a.yml', 'body', false)).toEqual({ ok: false, problems });
  });

  it('throws other write failures with their text', async () => {
    fetchMock.mockResolvedValue(reply(500, '{"error":"disk full"}'));

    await expect(host.writeFile('a.yml', 'b', false)).rejects.toThrow('disk full');
  });

  it('reads a 422 without problems as an empty list', async () => {
    fetchMock.mockResolvedValue(reply(422, '{}'));

    expect(await host.writeFile('a.yml', 'b', false)).toEqual({ ok: false, problems: [] });
  });

  it('throws the text of a 422 that is not json', async () => {
    fetchMock.mockResolvedValue(reply(422, 'bad gateway text'));

    await expect(host.writeFile('a.yml', 'b', false)).rejects.toThrow('bad gateway text');
  });
});
