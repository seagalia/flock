---
name: flock
description: Use when the user wants to create, change or understand flock load balancer resources (virtual servers and their virtual hosts, backends, certificates, VIPs, zones, instances) or write a flock controller or agent config.yml, with the flock CLI.
---

# flock

flock is a flexible load balancer orchestrator. A controller holds resources and agents program them. The `flock` binary is both the servers and the CLI. Everything below works with the commands of the installed binary; trust `flock explain` and `flock --help` over memory.

## Resource manifests

Manifests are YAML (or JSON) files of one resource, in the protobuf JSON format with proto field names: `certificate`, `backend`, `instance`, `secret`, `vip`, `vs` (virtual server, an HTTP one holds its virtual hosts and their routes in `http_routes_config.virtual_hosts`), `zone`.

`http_routes_config.external_auths` holds named policies, a virtual host names its default in `external_auth`, a route overrides with `external_auth_name` or `external_auth_disabled`. A policy asks an auth service (its `backend_uuid`) whether each request may pass: `flock explain flock.api.config.route.v1.ExternalAuth`.

1. **Context.** `flock me` tells who is logged in. If it fails, ask the user to `flock login -t <host:port>`, or to run `flock dev` in another terminal to try locally (logged in as admin, with a VIP `local` listening on every interface).
2. **Existing resources.** Manifests reference other resources by UUID (`backend_uuid`, instance `zone_uuid` and `vips_uuid`, VIP `virtual_servers_uuid`). List them with `--json`: `flock backends --json`, `flock certificates --json`, `flock secrets --json`, `flock vips --json`, `flock vss --json`, `flock zones --json`, `flock instances --json`. Read one with `flock backend <name> --json` (same for each resource).
3. **Learn the format.** `flock explain` lists resources and extensions. `flock explain <resource>` gives each field with its type, comment, enum values, oneof and validation rules. Follow the `→ flock explain ...` lines to go into sub messages. `typed_options` fields are `google.protobuf.Any`: pack one of the extensions named in their comment with `"@type": type.googleapis.com/<extension full name>` and its fields beside it, and explain it with `flock explain <extension full name>`.
4. **Write the file**, e.g. `vs.yml`. Leave `UUID` out on create. Only set what the user needs: defaults are applied.
5. **Validate offline** until it passes: `flock validate <resource> -f <file>`. Each line is `field.path: problem`. Unknown fields are errors too.
6. **Confirm, then apply.** Show the user the file and what it will do, and wait for their agreement. Then:
   - `flock create-<resource> -f <file> --force`
   - `flock update-<resource> <name|uuid> -f <file> --force` (the file holds the whole resource: start from `flock <resource> <name> --json`)

   `--force` skips the interactive confirmation the CLI can't show you; it is never a replacement for the user's agreement.

   To write several resources that refer to each other, put them in one multi-document file with a `kind` (`vs`, `backend` or `vip`) at the top of each document, refer to a resource by name with `name:<name>` where a UUID is expected, and run `flock apply -f <file or folder> --dry-run` to show the changes, then `flock apply -f <file or folder> --yes` once the user agreed. A document is matched by `name` (or its `UUID`) and created or updated, nothing is ever deleted.
7. **Check.** `flock <resource>s`, `flock status-vs`, `flock status-endpoints`, `flock show-linked-resource -r <resource> <name>`. `flock agents <instance>` shows the mode of each agent. Provisioners (mode `PROVISIONER`) are listed last and marked `(provisioner)`, and `flock status` and `flock check` ask them only for the resource types they report.

Order when building from scratch: zone (admin only) → instance in the zone → VIP: `flock register-vip -n <name> -i <instance>` takes a free ip in the zone of the instance and adds the VIP to it (`-z <zone>` or `-m key=value` (`--zone-metadata`) only takes the ip, give exactly one of `-i`, `-z`, `-m`; `--pool <name>` or `--pool-metadata key=value`, not both, choose the pool of the zone, its first `PUBLIC` pool otherwise), an admin can also `create-vip` and `flock add-vip-to-instance <instance> <vip>` → backend (backends and health check) → certificate (for TLS) → vs (for HTTP, its virtual hosts with routes whose action is `backend_uuid`) → attach the vs to a VIP by adding its UUID to the VIP's `virtual_servers_uuid` (`flock vip <name> --json > vip.json`, edit, `flock update-vip <name> -f vip.json --force`). The ips of a zone are in its `pools` (`name`, `privacy`, `metadata`, `subnets`, `ip_ranges`): `update-zone` refuses to change them, use `flock add-pool-to-zone <zone> -f pool.yml`, `flock update-pool-of-zone <zone> -f pool.yml` (matched by the pool `name`) and `flock delete-pool-from-zone <zone> <pool>` (admin only). The vips of an instance only change with `add-vip-to-instance` and `delete-vip-from-instance`: `update-instance` refuses them.

## Secrets

A secret holds sensitive values (passwords, tokens) under keys. Never put a value in a manifest, a command you show the user or a file you write when a secret can hold it.

- `flock create-secret -n <name> --data key=value --from-file key=<path>`: both flags repeat, `--data key=-` reads the value from stdin (once), which keeps it out of the shell history.
- `flock update-secret <name|uuid> --data key=value --remove-key key --force`: the keys you don't name keep their stored value.
- `flock secrets` lists names, keys and how many resources use each, `flock secret <name>` shows keys and metadata. Neither prints a value. `flock reveal-secret <name> [--key key]` does, and needs the owner role: run it only when the user asked for the value.
- `flock delete-secret <name|uuid>`.
- A resource points at a value of a secret instead of holding it. A header value takes `value_from_secret: {secret_uuid: <uuid>, key: <key>}` in place of `value` (EDS headers, health check headers, the request and response headers of the starling `HttpOptions` in `http_routes_config.typed_options`), and the EDS url password only exists as `url.user_info.password_from_secret` (a plain `password` is refused). A top-level metadata value takes `secret://<secret uuid>/<key>`. In a manifest, `secret_uuid: name:<secret name>` and `secret://name:<secret name>/<key>` name the secret, the cli sends its uuid.

## Controller and agent config.yml

These are the configs of `flock controller serve -c config.yml` and `flock agent starling start -c config.yml`, not resources.

- Reference: https://flock.seagalia.io/reference/controller-configuration/ and https://flock.seagalia.io/agents/starling/reference/configuration/. Samples: `controller/config.sample.yml` and `agent/starling/config.sample.yml` in https://github.com/seagalia/flock. After a `flock dev` run, the `*.yml` files of its data dir (`~/.flock/dev/` by default, `--data-dir`) are working configs to start from, one per component. `flock dev` is never for production: there, each component runs in its own container (`ghcr.io/seagalia/flock` for the starling agent, `ghcr.io/seagalia/flock:<version>-slim` for the others) with its own config.
- Check offline until it passes: `flock controller check-config -c config.yml`, `flock agent starling check-config -c config.yml`. Unknown keys are reported; files the config points to (certificates) must exist where the check runs, and the agent checks network interfaces exist, so run the agent check on the agent host.
- The F5 agent (BIG-IP through AS3) has its own config, sample `agent/f5/config.sample.yml`, checked with `flock agent f5 check-config -c config.yml` (the BIG-IP password comes from `password_file` or `FLOCK_BIGIP_PASSWORD`) and run with `flock agent f5 start -c config.yml`.

## Rules

- Never run `apply --yes`, `delete-*`, `update-*`, `create-*`, `register-vip`, `add-vip-to-instance`, `add-pool-to-zone`, `update-pool-of-zone` or `delete-pool-from-zone` without the user's agreement on that exact change.
- A resource whose metadata holds `flock.autoprovision: "true"` is locked, so `update-*`, `delete-*`, `apply` and the pool, `add-vip-to-instance`, `delete-vip-from-instance` and `register-vip --instance` commands refuse to change it, and no manifest may set that key. Change it where it was provisioned. An admin can still delete a whole locked resource with `delete-<resource> --force-locked`, and the provisioner may then create it again: use it only when the user asked for it.
- Prefer `--json` output when you need to read results.
- Don't guess field names or extension types: explain first, validate after.
