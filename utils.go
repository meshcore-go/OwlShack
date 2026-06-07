package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"
)

func loadOrCreateIdentity(path string) (meshcore.LocalIdentity, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return identityFromFile(data)
	}
	if !os.IsNotExist(err) {
		return meshcore.LocalIdentity{}, fmt.Errorf("reading key file: %w", err)
	}

	id, err := meshcore.GenerateLocalIdentity(nil)
	if err != nil {
		return meshcore.LocalIdentity{}, fmt.Errorf("generating identity: %w", err)
	}

	if err := writeIdentity(path, id); err != nil {
		return meshcore.LocalIdentity{}, err
	}

	return id, nil
}

func identityFromFile(data []byte) (meshcore.LocalIdentity, error) {
	seed, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return meshcore.LocalIdentity{}, fmt.Errorf("decoding key file: expected hex seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return meshcore.LocalIdentity{}, fmt.Errorf("key file: expected %d byte seed, got %d", ed25519.SeedSize, len(seed))
	}
	var s [ed25519.SeedSize]byte
	copy(s[:], seed)
	return meshcore.NewLocalIdentityFromSeed(s), nil
}

func writeIdentity(path string, id meshcore.LocalIdentity) error {
	seed := id.Seed()
	content := hex.EncodeToString(seed[:]) + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}
	return nil
}

func publicKeyHex(id meshcore.LocalIdentity) string {
	pk := id.PublicKey()
	return strings.ToUpper(hex.EncodeToString(pk[:]))
}

func channelFromRef(ref ChannelRef) (*meshcore.ChannelEntry, error) {
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

func resolvePathHashSize(configured *uint8, evt TriggerEvent) uint8 {
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
