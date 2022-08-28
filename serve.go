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
	// Setting Probe
	probe := cs.NewProbe(c)
	defer probe.Close()

	// Setting Cleaner
	cleaner := s.NewCleaner(c)
	defer cleaner.Close()

	// Setting Serve
	serve := cs.NewServe(probe, cleaner)

	// And SERVE!
	err := serve.Serve()
	if err != nil {
		log.WithError(err).Error("got server error")
	}
	return err
}
