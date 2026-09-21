package services

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

// When the cleaner drops a torrent's directory, everything the seeder had
// announced as complete in it (resource.cached) is gone at once, and the seeder
// itself will not say so: the torrent is not loaded, which is why it was the
// one to go. So the cleaner says it, for the whole torrent:
//
//	resource.uncached  {"resource_id": "<infohash>"}
//
// It speaks for THIS disk only. A consumer must not read it as "Webtor no
// longer has it": the same content may be stored elsewhere, which the cleaner
// cannot know.
//
// Best-effort: cleaning never waits for or fails on the message.
const (
	natsServiceHostFlag = "nats-service-host"
	natsServicePortFlag = "nats-service-port"

	uncachedSubject = "resource.uncached"
)

func RegisterCacheEventsFlags(f []cli.Flag) []cli.Flag {
	return append(f,
		cli.StringFlag{
			Name:   natsServiceHostFlag,
			Usage:  "nats service host; cache events are published when set",
			Value:  "",
			EnvVar: "NATS_SERVICE_HOST",
		},
		cli.IntFlag{
			Name:   natsServicePortFlag,
			Usage:  "nats service port",
			Value:  4222,
			EnvVar: "NATS_SERVICE_PORT",
		},
	)
}

type cachePublisher interface {
	Publish(subject string, data []byte) error
}

// CacheEvents publishes; a nil *CacheEvents is valid and publishes nothing.
type CacheEvents struct {
	p  cachePublisher
	nc *nats.Conn
}

// The store also holds things that are not torrents; only a directory named
// by an infohash is one.
var infohashRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func NewCacheEvents(c *cli.Context) *CacheEvents {
	host := c.String(natsServiceHostFlag)
	if host == "" {
		log.Info("cache events are off: nats service host is not set")
		return nil
	}
	url := fmt.Sprintf("nats://%s:%d", host, c.Int(natsServicePortFlag))
	nc, err := nats.Connect(url,
		nats.Name("torrent-web-seeder-cleaner"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(5*time.Second),
	)
	if err != nil {
		log.WithError(err).Warn("cache events are off: failed to set up nats connection")
		return nil
	}
	log.WithField("url", url).Info("cache events are published")
	return &CacheEvents{p: nc, nc: nc}
}

// Dropped reports that the torrent's directory was removed from this disk.
func (s *CacheEvents) Dropped(hash string) {
	if s == nil || s.p == nil || !infohashRe.MatchString(hash) {
		return
	}
	b, err := json.Marshal(map[string]string{"resource_id": hash})
	if err != nil {
		return
	}
	if err := s.p.Publish(uncachedSubject, b); err != nil {
		log.WithError(err).WithField("hash", hash).Warn("failed to publish cache event")
	}
}

func (s *CacheEvents) Close() {
	if s != nil && s.nc != nil {
		s.nc.Close()
	}
}
