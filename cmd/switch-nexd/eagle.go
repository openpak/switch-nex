// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openpak/switch-nex/eagle"
	"github.com/openpak/switch-nex/protocols"
)

// eagleTitles are the titles whose matches run through the Eagle relay: the
// environment prefix of their URL/listen variables, the relay profile and the
// default listener (the title's port base + 3).
var eagleTitles = map[string]struct {
	prefix  string
	profile eagle.Profile
	listen  string
}{
	"super-mario-bros-35": {"SMB35", eagle.SMB35, ":22163"},
	"pac-man-99":          {"PACMAN99", eagle.PacMan99, ":23143"},
}

// configureEagle binds the gameplay listener before installing any handoff
// hook. Hosting a nonexistent relay URL would turn every join into a timeout.
func configureEagle(env *protocols.Env, cfg *tls.Config) error {
	et, ok := eagleTitles[env.Name]
	if !ok {
		return nil
	}
	base := strings.TrimRight(os.Getenv(et.prefix+"_EAGLE_URL"), "/")
	if base == "" {
		log.Printf("[%s] eagle: disabled (%s_EAGLE_URL unset); lobbies only", env.Name, et.prefix)
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "wss" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s_EAGLE_URL must be an absolute wss URL without credentials or query", et.prefix)
	}
	addr := os.Getenv(et.prefix + "_EAGLE_LISTEN")
	if addr == "" {
		addr = et.listen
	}
	relay, err := eagle.NewServerProfile(et.profile)
	if err != nil {
		return err
	}
	ln, err := listenTitle(addr, cfg.Clone())
	if err != nil {
		return err
	}
	server := &http.Server{Handler: relay, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		// net/http's TLS error log carries raw client addresses; relay stage
		// logs below expose only gathering/node numbers and status.
		ErrorLog: log.New(io.Discard, "", 0)}
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[eagle] listener stopped: %v", err)
		}
	}()
	env.EagleTicket = func(gid uint32, pid uint64) (string, string, error) {
		token, err := relay.Ticket(gid, pid)
		return fmt.Sprintf("%s/%d", base, gid), token, err
	}
	env.EagleLeave = relay.Leave
	env.EagleClose = relay.CloseRoom
	log.Printf("[%s] eagle: gameplay listener %s enabled", env.Name, addr)
	return nil
}
