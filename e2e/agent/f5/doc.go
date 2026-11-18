// Package f5 holds the end to end scenarios of the F5 agent: the flock binary runs on the host against an in-process
// fake controller and a mock BIG-IP pair, and lab_test.go checks the declarations on a real BIG-IP when one is
// configured. They only build with the e2e build tag.
package f5
