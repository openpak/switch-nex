package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/openpak/switch-nex/internal/smm2store"
	"github.com/openpak/switch-nex/protocols"
)

// configureSMM2 gives Super Mario Maker 2 durable users and course storage when
// SMM2_DATABASE_URL is set, and serves the course site's read API on
// SMM2_API_ADDR. Unset or unreachable, the title refuses to start
// unless SWITCH_NEX_ALLOW_MEMORY=1 (then: in-memory profiles and empty lists).
//
//	SMM2_DATABASE_URL     postgres://... (database smm2, schema smm2)
//	SMM2_S3_ENDPOINT      bucket endpoint this process dials
//	SMM2_S3_PUBLIC_URL    base URL consoles are told (default: the endpoint)
//	SMM2_S3_BUCKET        default nex-smm2
//	SMM2_S3_ACCESS_KEY, SMM2_S3_SECRET_KEY
//	SMM2_API_ADDR, SMM2_API_KEY   read API listener and its X-Internal-Key
//
// Every value is passed through os.ExpandEnv.
func configureSMM2(env *protocols.Env) {
	// Values may name other variables ($PGPASSWORD, $MINIO_ROOT_USER) so the quadlet
	// need not carry a secret: the shared .env supplies them.
	getenv := func(k string) string { return os.ExpandEnv(os.Getenv(k)) }
	dsn := getenv("SMM2_DATABASE_URL")
	var st *smm2store.Store
	err := errors.New("SMM2_DATABASE_URL is not set")
	if dsn != "" {
		err = openWithRetry("smm2 store", dbAttempts, dbWait, func(ctx context.Context) (e error) {
			st, e = smm2store.Open(ctx, dsn)
			return e
		})
	}
	if err != nil {
		requireDB(env.Name, "smm2 store", err)
		return
	}
	pub := getenv("SMM2_S3_PUBLIC_URL")
	if pub == "" {
		pub = getenv("SMM2_S3_ENDPOINT")
	}
	bucket := getenv("SMM2_S3_BUCKET")
	if bucket == "" {
		bucket = "nex-smm2"
	}
	blobs := smm2store.NewBlobs(smm2store.S3Config{
		Endpoint: getenv("SMM2_S3_ENDPOINT"), PublicURL: pub, Bucket: bucket,
		Access: getenv("SMM2_S3_ACCESS_KEY"), Secret: getenv("SMM2_S3_SECRET_KEY"),
	})
	if err := blobs.EnsureBucket(); err != nil {
		log.Printf("[%s] smm2 bucket: %v (uploads will fail until it exists)", env.Name, err)
	}
	env.SMM2, env.SMM2Blobs = st, blobs
	log.Printf("[%s] smm2 course storage on (bucket %s)", env.Name, bucket)
	if addr := getenv("SMM2_API_ADDR"); addr != "" {
		api := &smm2store.API{Store: st, Blobs: blobs, Key: getenv("SMM2_API_KEY")}
		go func() { log.Printf("[%s] smm2 read API: %v", env.Name, http.ListenAndServe(addr, api.Handler())) }()
	}
}
