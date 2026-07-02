package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/dojo-product/team6/db"
	grpcclient "github.com/dojo-product/team6/grpc"
	"github.com/dojo-product/team6/httpapi"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bunrouter"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	database := bun.NewDB(sqldb, pgdialect.New())
	defer database.Close()

	if err := db.Migrate(context.Background(), database); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	cmsGRPCAddr := os.Getenv("CMS_GRPC_ADDR")
	if cmsGRPCAddr == "" {
		log.Fatal("CMS_GRPC_ADDR is required")
	}

	contentfulSpaceID := os.Getenv("CONTENTFUL_SPACE_ID")
	contentfulEnvironment := os.Getenv("CONTENTFUL_ENVIRONMENT")
	contentfulToken := os.Getenv("CONTENTFUL_ACCESS_TOKEN")

	grpcClient, err := grpcclient.NewClient(cmsGRPCAddr, contentfulSpaceID, contentfulEnvironment, contentfulToken)
	if err != nil {
		log.Fatalf("grpc client: %v", err)
	}

	apiKey := os.Getenv("OPENAI_API_KEY")

	router := bunrouter.New()
	httpapi.RegisterRoutes(router, database, grpcClient, apiKey)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	addr := fmt.Sprintf(":%s", port)
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("server: %v", err)
	}
}
