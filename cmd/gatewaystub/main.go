// Command gatewaystub runs the contract stub of GatewayAPI's control plane
// standalone: ws://<addr>/gateway/{module}/{instance}, a fake POST /oauth2/token
// (client_credentials for the configured audience) and GET /gateway/list with
// what the stub has seen. For running a GoAPI module without the real gateway.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/apostoldevel/go-platform/internal/gatewaystub"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4978", "listen address")
	secret := flag.String("secret", "stub-secret", "HMAC secret of the gateway audience")
	audience := flag.String("audience", "gateway-stub", "client_id of the gateway audience")
	interval := flag.Int("heartbeat", 5, "heartbeat_interval, seconds")
	flag.Parse()
	_, h := gatewaystub.Standalone(gatewaystub.Options{Secret: *secret, Audience: *audience, HeartbeatInterval: *interval})
	log.Printf("gateway stub on %s (audience %s)", *addr, *audience)
	log.Fatal(http.ListenAndServe(*addr, h))
}
