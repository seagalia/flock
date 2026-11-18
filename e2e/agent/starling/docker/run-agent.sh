#!/bin/sh
# keeps the container (and so its network namespace) alive across agent restarts,
# create /tmp/stop to stop restarting the agent

# same modules as assets/provision.sh does on hosts
for module in ip_vs ip_vs_rr ip_vs_wrr ip_vs_sh ip_vs_lc ip_vs_wlc ip_vs_mh nf_conntrack; do
  modprobe "$module" || echo "failed to load kernel module $module" >&2
done

while [ ! -f /tmp/stop ]; do
  /usr/local/bin/flock agent starling start -c /etc/flock/agent.yml
  sleep 1
done
exec sleep infinity
