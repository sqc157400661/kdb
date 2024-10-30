package config

var debug = false

func EnableDebug() {
	debug = true
}

func IsDebugEnabled() bool {
	return debug
}
