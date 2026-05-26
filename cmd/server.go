package cmd

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/render-oss/render-mcp-server/pkg/authn"
	"github.com/render-oss/render-mcp-server/pkg/cfg"
	"github.com/render-oss/render-mcp-server/pkg/client"
	"github.com/render-oss/render-mcp-server/pkg/deploy"
	"github.com/render-oss/render-mcp-server/pkg/httpcontext"
	"github.com/render-oss/render-mcp-server/pkg/keyvalue"
	"github.com/render-oss/render-mcp-server/pkg/logs"
	"github.com/render-oss/render-mcp-server/pkg/metrics"
	"github.com/render-oss/render-mcp-server/pkg/multicontext"
	"github.com/render-oss/render-mcp-server/pkg/owner"
	"github.com/render-oss/render-mcp-server/pkg/postgres"
	"github.com/render-oss/render-mcp-server/pkg/service"
	"github.com/render-oss/render-mcp-server/pkg/session"
	"github.com/render-oss/render-mcp-server/pkg/sre"
)

type Mode string

const (
	ModeSRE  Mode = "sre"
	ModeAPI  Mode = "api"
	ModeBoth Mode = "both"
)

func Serve(transport string, mode string) *server.MCPServer {
	m := Mode(mode)
	if m != ModeSRE && m != ModeAPI && m != ModeBoth {
		log.Fatalf("invalid mode %q: must be one of sre, api, both", mode)
	}

	s := server.NewMCPServer(
		"render-mcp-server",
		cfg.Version,
	)

	c, err := client.NewDefaultClient()
	if err != nil {
		panic(err)
	}

	if m == ModeAPI || m == ModeBoth {
		registerAPITools(s, c)
	}

	if m == ModeSRE || m == ModeBoth {
		registerSRETools(s, c)
	}

	if transport == "http" {
		var sessionStore session.Store
		if redisURL, ok := os.LookupEnv("REDIS_URL"); ok {
			log.Print("using Redis session store\n")
			sessionStore, err = session.NewRedisStore(redisURL)
			if err != nil {
				log.Fatalf("failed to initialize Redis session store: %v", err)
			}
		} else {
			log.Print("using in-memory session store\n")
			sessionStore = session.NewInMemoryStore()
		}
		streamableServer := server.NewStreamableHTTPServer(s, server.WithHTTPContextFunc(multicontext.MultiHTTPContextFunc(
			session.ContextWithHTTPSession(sessionStore),
			authn.ContextWithAPITokenFromHeader,
			httpcontext.ContextWithHTTPRequest,
		)))

		mux := http.NewServeMux()
		mux.Handle("/mcp", streamableServer)
		if token := os.Getenv("OPENAI_VERIFICATION_TOKEN"); token != "" {
			mux.HandleFunc("/.well-known/openai-apps-challenge", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.Write([]byte(token))
			})
		}

		httpServer := &http.Server{
			Addr:        ":10000",
			Handler:     mux,
			ReadTimeout: 5 * time.Second,
		}
		err := httpServer.ListenAndServe()
		if err != nil {
			log.Fatalf("Starting Streamable server: %v\n:", err)
		}
	} else {
		err := server.ServeStdio(s, server.WithStdioContextFunc(multicontext.MultiStdioContextFunc(
			session.ContextWithStdioSession,
			authn.ContextWithAPITokenFromConfig,
		)))
		if err != nil {
			log.Fatalf("Starting STDIO server: %v\n", err)
		}
	}

	return s
}

func registerAPITools(s *server.MCPServer, c *client.ClientWithResponses) {
	s.AddTools(owner.Tools(c)...)
	s.AddTools(service.Tools(c)...)
	s.AddTools(deploy.Tools(c)...)
	s.AddTools(postgres.Tools(c)...)
	s.AddTools(keyvalue.Tools(c)...)
	s.AddTools(logs.Tools(c)...)
	s.AddTools(metrics.Tools(c)...)
}

func registerSRETools(s *server.MCPServer, c *client.ClientWithResponses) {
	s.AddTools(sre.Tools(c)...)
	sre.AddResources(s, c)
}
