import { Injectable } from '@angular/core';

export interface FileProblem {
  file: string;
  index: number;
  field: string;
  message: string;
}

export type WriteResult =
  | { ok: true }
  | { ok: false; exists: true }
  | { ok: false; problems: FileProblem[] };

// CliHost talks to the file endpoints of `flock dashboard`, the local cli server the page is served by. The builder
// only writes new files there, it reads none.
@Injectable({ providedIn: 'root' })
export class CliHost {
  private cachedInfo?: Promise<{ cwd: string } | null>;
  private cachedStandaloneInfo?: Promise<{ controller: string } | null>;

  // info answers null when the page is not served by the cli, since a controller has no such path
  info(): Promise<{ cwd: string } | null> {
    this.cachedInfo ??= this.fetchInfo<{ cwd: string }>('/cli/v1/info');
    return this.cachedInfo;
  }

  // standaloneInfo answers null unless the page is served by `flock dashboard --standalone`
  standaloneInfo(): Promise<{ controller: string } | null> {
    this.cachedStandaloneInfo ??= this.fetchInfo<{ controller: string }>('/standalone/v1/info');
    return this.cachedStandaloneInfo;
  }

  async writeFile(path: string, content: string, overwrite: boolean): Promise<WriteResult> {
    const resp = await fetch(this.fileUrl(path, overwrite), { method: 'PUT', body: content });
    if (resp.status === 204) {
      return { ok: true };
    }
    if (resp.status === 409) {
      return { ok: false, exists: true };
    }
    if (resp.status === 422) {
      const text = await resp.text();
      try {
        const body = JSON.parse(text) as { problems?: FileProblem[] };
        return { ok: false, problems: body.problems ?? [] };
      } catch {
        throw new Error(text || resp.statusText);
      }
    }
    throw new Error(await errorText(resp));
  }

  private async fetchInfo<T>(url: string): Promise<T | null> {
    try {
      const resp = await fetch(url);
      return resp.status === 200 ? ((await resp.json()) as T) : null;
    } catch {
      return null;
    }
  }

  private fileUrl(path: string, overwrite = false): string {
    const query = new URLSearchParams({ path });
    if (overwrite) {
      query.set('overwrite', 'true');
    }
    return `/cli/v1/file?${query}`;
  }
}

// errorText is the `error` of a JSON body, or the plain text the server sent
async function errorText(resp: Response): Promise<string> {
  const text = await resp.text();
  try {
    const parsed = JSON.parse(text) as { error?: string };
    return parsed.error ?? text;
  } catch {
    return text || resp.statusText;
  }
}
