package app

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/meshcore-go/meshcore-bot/internal/api"
	"github.com/meshcore-go/meshcore-bot/internal/config"
	"github.com/meshcore-go/meshcore-bot/internal/node/companion"
	"github.com/meshcore-go/meshcore-bot/internal/store"
)

// repeaterReqTimeout bounds every repeater round-trip initiated from the API.
const repeaterReqTimeout = 10 * time.Second

// backend implements api.Backend for a generation of running companions. A new
// instance is built and installed on the server at startup and after each
// reload/reconnect, so it never has to mutate its companion set.
type backend struct {
	companions []*companion.Companion
	db         *store.Store
	reload     func() error
}

func newBackend(companions []*companion.Companion, db *store.Store, reload func() error) *backend {
	return &backend{companions: companions, db: db, reload: reload}
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
		RoomLogin: func(pubkeyHex, password string, syncSince uint32) (any, error) {
			return rm.SendRoomLogin(pubkeyHex, password, syncSince, repeaterReqTimeout)
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
	cfg, err := loadConfigFromDB(b.db)
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

	if err := saveConfig(b.db, cfg); err != nil {
		return err
	}

	slog.Info("config persisted with channel changes")
	return nil
}

// Ensure backend satisfies the api seam.
var _ api.Backend = (*backend)(nil)
