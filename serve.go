package main

import (
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	cs "github.com/webtor-io/common-services"
	s "github.com/webtor-io/torrent-web-seeder-cleaner/services"
)

func makeServeCMD() cli.Command {
	serveCmd := cli.Command{
		Name:    "serve",
		Aliases: []string{"s"},
		Usage:   "Serves cleaner service",
		Action:  serve,
	}
	configureServe(&serveCmd)
	return serveCmd
}

func configureServe(c *cli.Command) {
	c.Flags = cs.RegisterProbeFlags(c.Flags)
	c.Flags = s.RegisterCleanerFlags(c.Flags)
}

func serve(c *cli.Context) error {
	var servers []cs.Servable
	// Setting Probe
	probe := cs.NewProbe(c)
	if probe != nil {
		servers = append(servers, probe)
		defer probe.Close()
	}

	// Setting MultiCleaner
	cleaner := s.NewMultiCleaner(c)
	servers = append(servers, cleaner)
	defer cleaner.Close()

	// Setting Serve
	serve := cs.NewServe(servers...)

	// And SERVE!
	err := serve.Serve()
	if err != nil {
		log.WithError(err).Error("got server error")
	}
	return err
}
