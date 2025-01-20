package services

import (
	"os"

	"github.com/urfave/cli"
)

const (
	CleanerKeepFreeFlag = "keep-free"
	CleanerFreeFlag     = "free"
	DataDirFlag         = "data-dir"
)

func RegisterCleanerFlags(f []cli.Flag) []cli.Flag {
	return append(f,
		cli.StringFlag{
			Name:   CleanerKeepFreeFlag,
			Usage:  "keep free",
			Value:  "25%",
			EnvVar: "CLEANER_KEEP_FREE",
		},
		cli.StringFlag{
			Name:   CleanerFreeFlag,
			Usage:  "free",
			Value:  "35%",
			EnvVar: "CLEANER_FREE",
		},
		cli.StringFlag{
			Name:   DataDirFlag,
			Usage:  "data dir",
			Value:  os.TempDir(),
			EnvVar: "DATA_DIR",
		},
	)
}
