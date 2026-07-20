// Command mint signs a playback token for local testing. In production, tokens
// are issued by the identity/DRM service and this control plane only verifies
// them — mint exists purely so you can exercise the API by hand.
//
// Usage: go run ./cmd/mint -account acc1 -limit 2 [-ttl 1h]
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"livescale/internal/config"
	"livescale/internal/token"
)

func main() {
	account := flag.String("account", "acc1", "account id")
	limit := flag.Int("limit", 2, "device limit")
	ttl := flag.Duration("ttl", time.Hour, "token lifetime")
	flag.Parse()

	key := config.FromEnv().HMACKey
	tok := token.Sign(key, token.Claims{
		AccountID:   *account,
		DeviceLimit: *limit,
		Exp:         time.Now().Add(*ttl).Unix(),
		AssetScope:  "*",
	})
	fmt.Fprintln(os.Stdout, tok)
}
