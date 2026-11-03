package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/openpak/switch-nex/internal/mk8dstore"
	"github.com/openpak/switch-nex/internal/rankingsapi"
	"github.com/openpak/switch-nex/protocols"
)

// configureMK8D gives Mario Kart 8 Deluxe durable rankings, competitions,
// tournaments and ghosts when MK8D_DATABASE_URL is set, and serves the
// OpenKart read API on MK8D_API_ADDR. Unset or unreachable, the title refuses
// to start unless SWITCH_NEX_ALLOW_MEMORY=1 (then: empty boards, no DataStore).
//
//	MK8D_DATABASE_URL     postgres://... (database mk8d, schema mk8d)
//	MK8D_S3_ENDPOINT      bucket endpoint this process dials
//	MK8D_S3_PUBLIC_URL    base URL consoles are told (default: the endpoint)
//	MK8D_S3_BUCKET        default nex-mk8d
//	MK8D_S3_ACCESS_KEY, MK8D_S3_SECRET_KEY
//	MK8D_API_ADDR         read API listener (container network only)
//	RANKINGS_ADDR         OpenPak rankings contract listener, e.g. :9090 (off when empty)
//
// Every value is passed through os.ExpandEnv.
func configureMK8D(env *protocols.Env) {
	getenv := func(k string) string { return os.ExpandEnv(os.Getenv(k)) }
	dsn := getenv("MK8D_DATABASE_URL")
	var st *mk8dstore.Store
	err := errors.New("MK8D_DATABASE_URL is not set")
	if dsn != "" {
		err = openWithRetry("mk8d store", dbAttempts, dbWait, func(ctx context.Context) (e error) {
			st, e = mk8dstore.Open(ctx, dsn)
			return e
		})
	}
	if err != nil {
		requireDB(env.Name, "mk8d store", err)
		return
	}
	env.MK8D = st
	var blobs *mk8dstore.Blobs
	if getenv("MK8D_S3_ENDPOINT") != "" {
		pub := getenv("MK8D_S3_PUBLIC_URL")
		if pub == "" {
			pub = getenv("MK8D_S3_ENDPOINT")
		}
		bucket := getenv("MK8D_S3_BUCKET")
		if bucket == "" {
			bucket = "nex-mk8d"
		}
		blobs = mk8dstore.NewBlobs(mk8dstore.S3Config{
			Endpoint: getenv("MK8D_S3_ENDPOINT"), PublicURL: pub, Bucket: bucket,
			Access: getenv("MK8D_S3_ACCESS_KEY"), Secret: getenv("MK8D_S3_SECRET_KEY"),
		})
		if err := blobs.EnsureBucket(); err != nil {
			log.Printf("[%s] mk8d bucket: %v (ghost uploads will fail until it exists)", env.Name, err)
		}
		env.MK8DBlobs = blobs
		env.DataStore = protocols.DSMK8D
		log.Printf("[%s] mk8d ghost storage on (bucket %s)", env.Name, bucket)
	}
	log.Printf("[%s] mk8d rankings on", env.Name)
	rankingsapi.Serve(getenv("RANKINGS_ADDR"), "switch", mk8dstore.Rankings{Store: st})
	if addr := getenv("MK8D_API_ADDR"); addr != "" {
		api := &mk8dstore.API{Store: st, Blobs: blobs}
		go func() {
			log.Printf("[%s] mk8d read API on %s: %v", env.Name, addr, http.ListenAndServe(addr, api.Handler()))
		}()
	}
}
