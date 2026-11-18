import { signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, convertToParamMap } from '@angular/router';
import { create } from '@bufbuild/protobuf';
import { InstanceSchema } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { LogsStreamResponse } from '../../gen/flock/services/lookup/v1/lookup_pb';
import { Api } from '../core/api';
import { Instances } from '../core/instances';
import { Monitors } from './monitors';

// openStream is a log stream the spec feeds one response at a time, then ends
function openStream() {
  const pending: ((value: IteratorResult<LogsStreamResponse>) => void)[] = [];
  const stream: AsyncIterable<LogsStreamResponse> = {
    [Symbol.asyncIterator]: () => ({ next: () => new Promise((resolve) => pending.push(resolve)) }),
  };
  const line = (agentUuid: string, logLine: string) =>
    pending.shift()!({
      done: false,
      value: { event: { case: 'logStream', value: { agentUuid, logLine } } } as LogsStreamResponse,
    });
  const started = () =>
    pending.shift()!({
      done: false,
      value: { event: { case: 'started', value: {} } } as LogsStreamResponse,
    });
  const end = () => pending.shift()!({ done: true, value: undefined });
  return { stream, started, line, end };
}

// settle lets the for await loop of the page take the response the spec just gave
async function settle(fixture: { whenStable(): Promise<unknown> }) {
  await new Promise((resolve) => setTimeout(resolve));
  await fixture.whenStable();
}

describe('Monitors', () => {
  let logs: ReturnType<typeof openStream>;

  // xterm reads the device pixel ratio through matchMedia, which jsdom lacks
  beforeEach(() => {
    vi.stubGlobal('matchMedia', () => ({
      matches: false,
      addListener: () => undefined,
      removeListener: () => undefined,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    }));
  });
  afterEach(() => vi.unstubAllGlobals());

  async function render() {
    logs = openStream();
    TestBed.configureTestingModule({
      providers: [
        { provide: Api, useValue: { lookup: { getLogsStream: () => logs.stream } } },
        {
          provide: Instances,
          useValue: {
            load: async () => [],
            all: signal([]),
            current: signal(create(InstanceSchema, { UUID: 'i1', name: 'edge' })),
            agents: signal([]),
            choose: vi.fn(),
          },
        },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { queryParamMap: convertToParamMap({}) } },
        },
      ],
    });
    const fixture = TestBed.createComponent(Monitors);
    await settle(fixture);
    return fixture;
  }

  it('shows a loader until the first log line', async () => {
    const fixture = await render();
    const element = fixture.nativeElement as HTMLElement;
    expect(element.querySelector('.waiting')?.textContent).toContain('Waiting for the logs');

    logs.started();
    await settle(fixture);
    expect(element.querySelector('.waiting')).not.toBeNull();

    logs.line('a1', 'first line');
    await settle(fixture);

    expect(element.querySelector('.waiting')).toBeNull();
  });

  it('drops the loader when the stream ends without a line', async () => {
    const fixture = await render();

    logs.end();
    await settle(fixture);

    expect((fixture.nativeElement as HTMLElement).querySelector('.waiting')).toBeNull();
  });
});
