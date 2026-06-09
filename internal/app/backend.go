package app

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/meshcore-go/meshcore-bot/internal/api"
	"github.com/meshcore-go/meshcore-bot/internal/config"
	"github.com/meshcore-go/meshcore-bot/internal/node/companion"
)

// repeaterReqTimeout bounds every repeater round-trip initiated from the API.
const repeaterReqTimeout = 10 * time.Second

// backend implements api.Backend for a generation of running companions. A new
// instance is built and installed on the server at startup and after each
// reload/reconnect, so it never has to mutate its companion set.
type backend struct {
	companions []*companion.Companion
	cfg        *config.Config
	configPath string
	reload     func() error
}

func newBackend(companions []*companion.Companion, cfg *config.Config, configPath string, reload func() error) *backend {
	return &backend{companions: companions, cfg: cfg, configPath: configPath, reload: reload}
}

func (b *backend) find(name string) (*companion.Companion, bool) {
	for _, c := range b.companions {
		if c.Name() == name {
			return c, true
		}
	}
	return nil, false
}

func (b *backend) Companions() []api.CompanionInfo {
	infos := make([]api.CompanionInfo, 0, len(b.companions))
	for _, c := range b.companions {
		channels := make([]api.ChannelInfo, 0)
		for _, ch := range c.Node().Channels() {
			if ch == nil {
				continue
			}
			channels = append(channels, api.ChannelInfo{Name: ch.Name, PSK: ch.PSK[:]})
		}
		lat, lon := c.LatLon()
		infos = append(infos, api.CompanionInfo{
			Name:      c.Name(),
			PubKey:    hex.EncodeToString(c.Node().Identity().Identity.PublicKeyBytes()),
			PeerCount: c.Node().Peers().Count(),
			Lat:       lat,
			Lon:       lon,
			Channels:  channels,
		})
	}
	return infos
}

// ChannelByHash resolves a channel hash against the companions' live channels,
// so newly added channels are visible without rebuilding the backend.
func (b *backend) ChannelByHash(hash byte) *api.ChannelInfo {
	for _, c := range b.companions {
		for _, ch := range c.Node().Channels() {
			if ch != nil && ch.Hash == hash {
				return &api.ChannelInfo{Name: ch.Name, PSK: ch.PSK[:]}
			}
		}
	}
	return nil
}

func (b *backend) Companion(name string) (api.MessageSender, api.DMSender, bool) {
	c, ok := b.find(name)
	if !ok {
		return nil, nil, false
	}
	return c.SendChannelMessage, c.SendContactMessage, true
}

func (b *backend) ChannelMutator(name string) (api.ChannelAdder, api.ChannelRemover, bool) {
	c, ok := b.find(name)
	if !ok {
		return nil, nil, false
	}
	adder := func(chName, privateKey string) error {
		return c.AddChannel(config.ChannelRef{Name: chName, PrivateKey: privateKey})
	}
	return adder, c.RemoveChannel, true
}

func (b *backend) RenameChannel(companionName, oldName, newName string) error {
	c, ok := b.find(companionName)
	if !ok {
		return fmt.Errorf("companion %q not found", companionName)
	}
	return c.RenameChannel(oldName, newName)
}

func (b *backend) TraceSender(name string) (api.TraceSender, bool) {
	c, ok := b.find(name)
	if !ok {
		return nil, false
	}
	return c.SendTrace, true
}

func (b *backend) Repeater(name string) (*api.RepeaterOps, bool) {
	c, ok := b.find(name)
	if !ok {
		return nil, false
	}
	rm := c.Repeaters()
	return &api.RepeaterOps{
		Login: func(pubkeyHex, password string) (any, error) {
			return rm.SendLogin(pubkeyHex, password, repeaterReqTimeout)
		},
		StatusReq: func(pubkeyHex string) (any, error) {
			return rm.SendStatusReq(pubkeyHex, repeaterReqTimeout)
		},
		CLI: func(pubkeyHex, command string) (string, error) {
			return rm.SendCLI(pubkeyHex, command, repeaterReqTimeout)
		},
		Session: func(pubkeyHex string) any {
			return rm.Session(pubkeyHex)
		},
		Logout: func(pubkeyHex string) {
			rm.Logout(pubkeyHex)
		},
		PathGet: func(pubkeyHex string) (any, error) {
			return rm.GetPeerPath(pubkeyHex)
		},
		PathReset: func(pubkeyHex string) error {
			return rm.ResetPeerPath(pubkeyHex)
		},
		PathSet: func(pubkeyHex, pathHex string, pathHashSize int) error {
			return rm.SetPeerPath(pubkeyHex, pathHex, pathHashSize)
		},
		NeighborsReq: func(pubkeyHex string, count uint8, offset uint16) (any, error) {
			return rm.SendNeighborsReq(pubkeyHex, count, offset, repeaterReqTimeout)
		},
		OwnerInfoReq: func(pubkeyHex string) (any, error) {
			return rm.SendOwnerInfoReq(pubkeyHex, repeaterReqTimeout)
		},
		TelemetryReq: func(pubkeyHex string) (any, error) {
			return rm.SendTelemetryReq(pubkeyHex, repeaterReqTimeout)
		},
		ContactTelemetryReq: func(pubkeyHex string) (any, error) {
			return rm.SendContactTelemetryReq(pubkeyHex, repeaterReqTimeout)
		},
		AccessList: func(pubkeyHex string) (any, error) {
			return rm.SendAccessListReq(pubkeyHex, repeaterReqTimeout)
		},
		SetPerm: func(pubkeyHex, targetPubkeyHex string, perms uint8) error {
			return rm.SetAccessPerm(pubkeyHex, targetPubkeyHex, perms, repeaterReqTimeout)
		},
	}, true
}

// PersistChannels writes each companion's current standalone channels back to
// the config file, leaving the rest of the file intact.
func (b *backend) PersistChannels() error {
	if b.configPath == "" {
		return fmt.Errorf("config path not set")
	}

	cfg, _, err := config.LoadFromPath(b.configPath)
	if err != nil {
		return fmt.Errorf("reading config for persist: %w", err)
	}

	for i, comp := range b.companions {
		if i >= len(cfg.Companions) {
			break
		}
		channels := comp.StandaloneChannels()
		if len(channels) > 0 {
			cl := config.ChannelList(channels)
			cfg.Companions[i].Channels = &cl
		} else {
			cfg.Companions[i].Channels = nil
		}
	}

	data, err := config.Marshal(b.configPath, cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	if err := os.WriteFile(b.configPath, data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	slog.Info("config persisted with channel changes")
	return nil
}

func (b *backend) Config() any {
	if b.configPath == "" {
		return b.cfg
	}
	current, _, err := config.LoadFromPath(b.configPath)
	if err != nil {
		return b.cfg
	}
	return current
}

func (b *backend) UpdateConfig(input map[string]any) error {
	if b.configPath == "" {
		return fmt.Errorf("config path not set")
	}

	raw, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("invalid config data: %w", err)
	}

	var newCfg config.Config
	if err := json.Unmarshal(raw, &newCfg); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	if err := newCfg.Validate(); err != nil {
		return err
	}

	newCfg.ApplyDefaults()

	data, err := config.Marshal(b.configPath, &newCfg)
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}

	if err := os.WriteFile(b.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	if b.reload != nil {
		if err := b.reload(); err != nil {
			return fmt.Errorf("config saved but reload failed: %w", err)
		}
	}

	slog.Info("config updated via API")
	return nil
}

// Ensure backend satisfies the api seam.
var _ api.Backend = (*backend)(nil)
