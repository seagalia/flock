# Flock dashboard

The web dashboard of flock, served by the controller at `/dashboard/` when `server.dashboard.enabled` is set (always on with `flock dev`). It shows virtual servers, routes, backends, VIPs, certificates, instances and zones, the status of the resources on the agents and their live logs. It is read-only for now.

Angular and Angular Material at their latest versions, `@connectrpc/connect-web` for the API, Node 24 (`.nvmrc`).

## Develop

```bash
flock dev            # in another terminal: controller on https://127.0.0.1:8443, admin/admin
make webui-dev       # http://localhost:4200/dashboard/, the api is proxied to the flock dev controller (proxy.conf.json)
make webui-test      # eslint, prettier check, vitest
npm run format       # prettier --write
```

## Build

`make webui` runs `npm ci && npm run build` and writes `dist/app/browser`. `embed.go` embeds `dist/` in the flock binary, so rebuild flock afterwards. Only `dist/.gitkeep` is committed: flock builds without node, and `/dashboard/` then answers 503 until the dashboard is built. Releases build it in goreleaser's `before` hook.

## Code

- `src/gen/`: messages and services generated from `api/` by `make generate` (protobuf-es). Never edit them by hand.
- `src/app/core/`: the connect clients and their interceptors (`api.ts`), the session (`session.ts`), resource kinds and list filters (`resources.ts`), labels of the api enums and oneofs (`display.ts`), the light/dark theme (`theme.ts`).
- `src/app/ui/`: `ResourceTable`, the table of every resource page, and the blocks of the expanded rows (config cards, endpoint states, links and permissions, raw details side panel).
- `src/app/pages/`: one component per route, loaded on first visit.
- `src/styles.scss`: Material 3 theme and the `--flock-*` tokens, every color a `light-dark()` pair. `src/styles/_theme-colors.scss` comes from `ng generate @angular/material:theme-color` with the Seagalia palette (azure `#0B8FDE`, navy `#073B74`, coral `#F4774B`, ink `#0B2F55`).
- `public/brand/`: Flock logos from the seagalia-brand kit (`exports/flock`), light and `-dark` variants.
