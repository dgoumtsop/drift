package main
import (
	"fmt"
	"log"
	"net/http"

	"github.com/dgoumtsop/drift/internal/config"
	"github.com/dgoumtsop/drift/internal/proxy"
) 

func main () {
	cfg, err := config.Load()
	if err != nil{
		log.Fatalf("Failed to load config: %v", err)
	}

	// create the reverse proxy 
	reverseProxy, err := proxy.New(cfg.BackendURL)
	if err != nil {
		log.Fatalf("Failed to create proxy: %v", err)
	}

	// Setting up HTTP Server
	http.HandleFunc("/", reverseProxy.ServeHTTP)

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Starting Drift gateway on %s", addr)
	log.Printf("Proxying to: %s", cfg.BackendURL)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
