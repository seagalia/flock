# Grafana dashboards

Dashboards are grouped by flock component, one directory each under `dashboards/`. Grafana's file provisioning turns
each directory into a folder when `foldersFromFilesStructure` is on.

## `dashboards/starling/`

| File                           | Dashboard                         | Shows                                                                                                                                      |
|--------------------------------|-----------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `global_instance.json`         | Starling / Global                 | health of every instance (unhealthy agents and endpoints, 5xx share, agent failures, certificate expiry), top 20 instances by traffic       |
| `instance_traffic.json`        | Starling / Instance Traffic       | one instance: error shares by virtual server, backend health, upstream failures, retries, TLS errors and certificates, traffic by agent     |
| `virtual_server_traffic.json`  | Starling / Virtual Server Traffic | one virtual server: status classes, healthy endpoints, upstream failures, backend and endpoint states, downstream and upstream traffic       |
| `instance_system_details.json` | Starling / Instance System        | agent health and resources, configuration failures, controller connection, version drift across agents, VIP advertisement (BGP, ARP/NDP)  |

The health rows sit at the top of each dashboard. Error panels only draw the series that moved, so an empty error panel
means nothing failed.

## `dashboards/controller/`

| File              | Dashboard  | Shows                                                                                                                    |
|-------------------|------------|--------------------------------------------------------------------------------------------------------------------------|
| `controller.json` | Controller | API calls and errors per service and method, open streams (agents subscribed), logins and authorization failures, locked out users, agents the controller can't reach or scrape, database status and connection pool, resources by type, process |

Its `controller` variable picks among the controllers by their scrape address (`instance`). It reads the series of the
controller's own `/metrics`, recognized by the absence of `instance_uuid`.

Every dashboard has a `datasource` variable, pick the Prometheus that scrapes flock.

## What Prometheus must scrape

The dashboards read two kinds of series.

The controller's own `/metrics` gives `flock_controller_store_info` and `flock_controller_foreign_store_info`. The
dashboards use them to show names instead of uuids and to chain the variables (instance, VIP, virtual server, backend).

The agents' metrics come through the controller at `/metrics/<instance uuid>`. The controller adds `agent_name`,
`agent_uuid`, `agent_host`, `agent_type` and sets `instance` to the agent host. Prometheus must add an `instance_uuid`
label with the uuid of the instance, every agent query filters on it. Keep the labels the controller sets with
`honor_labels: true`.

```yaml
scrape_configs:
  - job_name: flock_controller
    scheme: https
    tls_config:
      ca_file: /etc/flock/ca.pem
    static_configs:
      - targets: ["controller.example.com:8443"]

  - job_name: flock_instances
    scheme: https
    honor_labels: true
    tls_config:
      ca_file: /etc/flock/ca.pem
    static_configs:
      - targets: ["controller.example.com:8443"]
        labels:
          __metrics_path__: /metrics/b7009f9e-d427-4db4-aa98-a41c7d7cb599
          instance_uuid: b7009f9e-d427-4db4-aa98-a41c7d7cb599
```

One target per instance. `GET /metrics/instances-info` on the controller lists the instances with their uuid, name and
metrics path, so a small job can write a `file_sd_configs` file from it instead of a static list.

## IPVS and host panels

The starling agent doesn't export IPVS counters nor host resources. These panels read the metrics
of [node_exporter](https://github.com/prometheus/node_exporter) instead.

- The IPVS rows of the Global, Instance Traffic and Virtual Server Traffic dashboards use `node_ipvs_*`.
- The host panels of Instance System (cpu, memory, load, swap, network, and the RAM share of the agent and of envoy) use
  `node_cpu_*`, `node_memory_*`, `node_load15` and `node_network_*`.

Run node_exporter on every agent node and declare it as a proxy target of the agent, so its metrics go through the
agent's `/metrics` and the controller's `/metrics/<instance uuid>` with the same labels as the rest:

```yaml
metrics:
  proxy:
    targets:
      - name: node-exporter
        url: "http://127.0.0.1:9100/metrics"
```

The ipvs collector is on by default on linux. It reads `/proc/net/ip_vs`, which is per network namespace, so
node_exporter must run in the host's network namespace (`--net=host` for a container) to see the virtual servers the
agent programs. Keep the default labels of the collector (`--collector.ipvs.backend-labels`), the panels group on
`proto`, `local_address` and `local_port`.

node_exporter knows IPVS services by address and port, not by flock virtual server. The Instance Traffic dashboard lists
them as `proto address:port`. The Virtual Server Traffic dashboard shows the connections on the IP of the selected VIP,
taken from `flock_agent_announce_vips`, so only agents announcing that VIP count.

## Packet panels

The "packets per second" panels read `flock_agent_l3logs_*`, exported only when l3logs runs, which means a linux agent
without `disable_l3_log: true`. Outgoing packets carry no virtual server, so they are shown by agent only.
