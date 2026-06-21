package config

import (
	"errors"
	"flag"
	"fmt"
)

const (
	DefaultPort     = 8443
	DefaultCertFile = "certs/server.crt"
	DefaultKeyFile  = "certs/server.key"
)

type Config struct {
	Port     int
	Password string
	CertFile string
	KeyFile  string
}

func Load(args []string) (Config, error) {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	port := fs.Int("port", DefaultPort, "HTTPS listen port")
	password := fs.String("password", "", "shared password for clients and agents")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if *password == "" {
		return Config{}, errors.New("missing required --password")
	}
	if *port <= 0 || *port > 65535 {
		return Config{}, fmt.Errorf("invalid --port %d", *port)
	}

	return Config{
		Port:     *port,
		Password: *password,
		CertFile: DefaultCertFile,
		KeyFile:  DefaultKeyFile,
	}, nil
}
