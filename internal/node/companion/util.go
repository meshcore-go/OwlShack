package companion

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/trigger"
	meshcore "github.com/meshcore-go/meshcore-go"
)

func identityFromHexSeed(seedHex string) (meshcore.LocalIdentity, error) {
	return config.LocalIdentityFromHex(seedHex)
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
