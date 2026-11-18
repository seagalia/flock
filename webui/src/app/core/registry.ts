import { createRegistry } from '@bufbuild/protobuf';
import { file_flock_api_config_agent_v1_agent } from '../../gen/flock/api/config/agent/v1/agent_pb';
import { file_flock_api_config_agent_v1_status } from '../../gen/flock/api/config/agent/v1/status_pb';
import { file_flock_api_config_cert_v1_cert } from '../../gen/flock/api/config/cert/v1/cert_pb';
import { file_flock_api_config_backend_v1_backend } from '../../gen/flock/api/config/backend/v1/backend_pb';
import { file_flock_api_config_instance_v1_instance } from '../../gen/flock/api/config/instance/v1/instance_pb';
import { file_flock_api_config_permission_v1_permission } from '../../gen/flock/api/config/permission/v1/permission_pb';
import { file_flock_api_config_route_v1_virtual_host } from '../../gen/flock/api/config/route/v1/virtual_host_pb';
import { file_flock_api_config_vip_v1_vip } from '../../gen/flock/api/config/vip/v1/vip_pb';
import { file_flock_api_config_vs_v1_virtual_server } from '../../gen/flock/api/config/vs/v1/virtual_server_pb';
import { file_flock_api_config_zone_v1_zone } from '../../gen/flock/api/config/zone/v1/zone_pb';
import { file_flock_extensions_announces_starling_announce_v1_announce } from '../../gen/flock/extensions/announces/starling_announce/v1/announce_pb';
import { file_flock_extensions_backend_starling_options_v1_backend_options } from '../../gen/flock/extensions/backend/starling_options/v1/backend_options_pb';
import { file_flock_extensions_common_starling_options_v1_http_protocol } from '../../gen/flock/extensions/common/starling_options/v1/http_protocol_pb';
import { file_flock_extensions_limit_starling_limit_v1_limit } from '../../gen/flock/extensions/limit/starling_limit/v1/limit_pb';
import { file_flock_extensions_route_starling_route_v1_ratelimit } from '../../gen/flock/extensions/route/starling_route/v1/ratelimit_pb';
import { file_flock_extensions_route_starling_route_v1_virtual_host_route } from '../../gen/flock/extensions/route/starling_route/v1/virtual_host_route_pb';
import { file_flock_extensions_vs_starling_options_v1_route_options } from '../../gen/flock/extensions/vs/starling_options/v1/route_options_pb';

// Resources carry google.protobuf.Any (typed_options, typed_announce, status resources): unpacking
// them, or printing them as JSON, needs every message they can hold.
export const registry = createRegistry(
  file_flock_api_config_agent_v1_agent,
  file_flock_api_config_agent_v1_status,
  file_flock_api_config_cert_v1_cert,
  file_flock_api_config_backend_v1_backend,
  file_flock_api_config_instance_v1_instance,
  file_flock_api_config_permission_v1_permission,
  file_flock_api_config_route_v1_virtual_host,
  file_flock_api_config_vip_v1_vip,
  file_flock_api_config_vs_v1_virtual_server,
  file_flock_api_config_zone_v1_zone,
  file_flock_extensions_announces_starling_announce_v1_announce,
  file_flock_extensions_backend_starling_options_v1_backend_options,
  file_flock_extensions_common_starling_options_v1_http_protocol,
  file_flock_extensions_limit_starling_limit_v1_limit,
  file_flock_extensions_route_starling_route_v1_ratelimit,
  file_flock_extensions_route_starling_route_v1_virtual_host_route,
  file_flock_extensions_vs_starling_options_v1_route_options,
);
