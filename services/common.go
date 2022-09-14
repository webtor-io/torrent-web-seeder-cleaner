package services

import (
	"os"

	"github.com/urfave/cli"
)

const (
	CLEANER_KEEP_FREE_FLAG = "keep-free"
	DATA_DIR_FLAG          = "data-dir"
)

func RegisterCleanerFlags(f []cli.Flag) []cli.Flag {
	return append(f,
		cli.StringFlag{
			Name:   CLEANER_KEEP_FREE_FLAG,
			Usage:  "keep free",
			Value:  "35%",
			EnvVar: "CLEANER_KEEP_FREE",
		},
		cli.StringFlag{
			Name:   DATA_DIR_FLAG,
			Usage:  "data dir",
			Value:  os.TempDir(),
			EnvVar: "DATA_DIR",
		},
	)
}
