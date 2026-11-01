package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/openpak/switch-nex/internal/smm2store"
	"github.com/openpak/switch-nex/protocols"
)

// configureSMM2 gives Super Mario Maker 2 durable users and course storage when
// SMM2_DATABASE_URL is set, and serves the course site's read API on
// SMM2_API_ADDR. Unset, the title keeps its in-memory profiles and empty lists.
//
//	SMM2_DATABASE_URL     postgres://... (database smm2, schema smm2)
//	SMM2_S3_ENDPOINT      bucket endpoint this process dials
//	SMM2_S3_PUBLIC_URL    base URL consoles are told (default: the endpoint)
//	SMM2_S3_BUCKET        default nex-smm2
//	SMM2_S3_ACCESS_KEY, SMM2_S3_SECRET_KEY
//	SMM2_API_ADDR, SMM2_API_KEY   read API listener and its X-Internal-Key
func configureSMM2(env *protocols.Env) {
	dsn := os.Getenv("SMM2_DATABASE_URL")
	if dsn == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := smm2store.Open(ctx, dsn)
	if err != nil {
		log.Printf("[%s] smm2 store: %v (profiles stay in memory, no course storage)", env.Name, err)
		return
	}
	pub := os.Getenv("SMM2_S3_PUBLIC_URL")
	if pub == "" {
		pub = os.Getenv("SMM2_S3_ENDPOINT")
	}
	bucket := os.Getenv("SMM2_S3_BUCKET")
	if bucket == "" {
		bucket = "nex-smm2"
	}
	blobs := smm2store.NewBlobs(smm2store.S3Config{
		Endpoint: os.Getenv("SMM2_S3_ENDPOINT"), PublicURL: pub, Bucket: bucket,
		Access: os.Getenv("SMM2_S3_ACCESS_KEY"), Secret: os.Getenv("SMM2_S3_SECRET_KEY"),
	})
	if err := blobs.EnsureBucket(); err != nil {
		log.Printf("[%s] smm2 bucket: %v (uploads will fail until it exists)", env.Name, err)
	}
	env.SMM2, env.SMM2Blobs = st, blobs
	log.Printf("[%s] smm2 course storage on (bucket %s)", env.Name, bucket)
	if addr := os.Getenv("SMM2_API_ADDR"); addr != "" {
		api := &smm2store.API{Store: st, Blobs: blobs, Key: os.Getenv("SMM2_API_KEY")}
		go func() { log.Printf("[%s] smm2 read API: %v", env.Name, http.ListenAndServe(addr, api.Handler())) }()
	}
}
