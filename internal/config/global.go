package config

type DBConfig struct {
	RootUser     string `json:"root_user" yaml:"root_password"`
	RootPassword string `json:"root_password" yaml:"root_password"`
	ReplUser     string `yaml:"repl_user" json:"repl_user"`
	ReplPassword string `yaml:"repl_password" json:"repl_password"`
}
type GlobalConfig struct {
	DB DBConfig `json:"db" yaml:"db"`
}
