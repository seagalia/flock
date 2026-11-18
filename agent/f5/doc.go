// Package f5 is the F5 agent, a bridge between the flock controller and BIG-IP devices that declares the flock
// resources with AS3. It runs off the BIG-IP and changes nothing on its own host.
//
// The F5 agent is still very experimental: do not run it against a production BIG-IP. Its behavior, its configuration
// and the AS3 declarations it renders may change in any release, and it was only checked with dry-run declarations on
// an unlicensed lab device.
//
// The subpackages:
//   - app wires the agent and runs it (flock agent f5 start).
//   - handlers receive the resources of the controller, the backend handler also discovers the endpoints the BIG-IP
//     can't resolve itself.
//   - reconciler renders every tenant from the stores and posts the ones that changed, one at a time.
//   - as3 turns the resources of one partition into an AS3 declaration, with no I/O.
//   - bigip calls the iControl REST and AS3 APIs of an HA pair, bigipmock fakes them for the specs and the e2e suite.
//   - agentserv serves the agent status, the resource status and the logs to the controller.
//   - config reads the configuration file.
package f5
