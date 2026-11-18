package config

type AdminConfig struct {
	// Users defined as admin users
	Users []string `yaml:"users"`
	// Groups whose members are admin users
	Groups []string `yaml:"groups"`
}
