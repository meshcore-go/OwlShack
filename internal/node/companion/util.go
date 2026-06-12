package companion

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/meshcore-go/meshcore-bot/internal/config"
	"github.com/meshcore-go/meshcore-bot/internal/trigger"
	meshcore "github.com/meshcore-go/meshcore-go"
)

func identityFromHexSeed(seedHex string) (meshcore.LocalIdentity, error) {
	seed, err := hex.DecodeString(strings.TrimSpace(seedHex))
	if err != nil {
		return meshcore.LocalIdentity{}, fmt.Errorf("privateKey must be a hex seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return meshcore.LocalIdentity{}, fmt.Errorf("privateKey: expected %d byte seed, got %d", ed25519.SeedSize, len(seed))
	}
	var s [ed25519.SeedSize]byte
	copy(s[:], seed)
	return meshcore.NewLocalIdentityFromSeed(s), nil
}

func channelFromRef(ref config.ChannelRef) (*meshcore.ChannelEntry, error) {
	if ref.PrivateKey != "" {
		psk, err := hex.DecodeString(ref.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("invalid hex privateKey for channel %q: %w", ref.Name, err)
		}
		return meshcore.NewChannelFromPSK(ref.Name, psk)
	}
	if strings.EqualFold(ref.Name, "Public") {
		return meshcore.NewChannelFromBase64("Public", "izOH6cXN6mrJ5e26oRXNcg==")
	}
	nCh := meshcore.NormalizeHashtag(ref.Name)
	return meshcore.NewChannelFromHashtag(nCh), nil
}

func resolvePathHashSize(configured *uint8, evt trigger.Event) uint8 {
	if configured == nil {
		return 1
	}
	if *configured >= 1 && *configured <= 4 {
		return *configured
	}
	if incoming, ok := evt.Data["PathHashSize"].(uint8); ok && incoming >= 1 && incoming <= 4 {
		return incoming
	}
	return 1
}
