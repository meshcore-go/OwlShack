package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/meshcore-go/meshcore-bot/api"
	"github.com/meshcore-go/meshcore-bot/store"
	"github.com/meshcore-go/meshcore-bot/web"
	meshcore "github.com/meshcore-go/meshcore-go"
	companionClient "github.com/meshcore-go/meshcore-go/companion/client"
	companionTransport "github.com/meshcore-go/meshcore-go/companion/transport"
	"github.com/meshcore-go/meshcore-go/hardware"
	kissTransport "github.com/meshcore-go/meshcore-go/hardware/transport"
	"github.com/meshcore-go/meshcore-go/node"
	"github.com/pelletier/go-toml/v2"
	flag "github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

var version = "dev"

const LevelTrace = slog.Level(-8)

var defaultConfigNames = []string{
	"config.toml",
	"config.yaml",
	"config.yml",
	"config.json",
}

type closerFunc func()

func (f closerFunc) Close() error { f(); return nil }

type modemState struct {
	modem       node.Modem
	companionCl *companionClient.Client
	radioConfig *hardware.RadioConfig
	stats       StatsProvider
	recvErrors  *atomic.Uint64
	closers     []io.Closer
	watcherDone chan struct{}
}

func (m *modemState) Close() {
	if m.watcherDone != nil {
		select {
		case <-m.watcherDone:
		default:
			close(m.watcherDone)
		}
	}
	for i := len(m.closers) - 1; i >= 0; i-- {
		m.closers[i].Close()
	}
}

// startDeadWatcher spawns a goroutine that signals reconnectCh once when the
// underlying modem's read loop exits. No-op for modems that don't expose
// Dead() (e.g. the companion modem).
func (m *modemState) startDeadWatcher(reconnectCh chan<- struct{}) {
	d, ok := m.modem.(interface{ Dead() <-chan struct{} })
	if !ok {
		return
	}
	m.watcherDone = make(chan struct{})
	done := m.watcherDone
	dead := d.Dead()
	go func() {
		select {
		case <-dead:
			select {
			case reconnectCh <- struct{}{}:
			default:
			}
		case <-done:
		}
	}()
}

func main() {
	configPath := flag.StringP("config", "c", "", "path to config file (toml, yaml, or json)")
	showVersion := flag.BoolP("version", "V", false, "print version and exit")
	verbosity := flag.CountP("verbose", "v", "increase log verbosity (-v=debug, -vv=trace, -vvv=trace+)")
	flag.Parse()

	if *showVersion {
		fmt.Println("meshcore-bot", version)
		return
	}

	cfg, resolvedPath, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(cfg.Companions) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no companions configured")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *verbosity > 0 {
		level := slog.LevelDebug
		if *verbosity >= 2 {
			level = LevelTrace
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == slog.LevelKey && a.Value.Any().(slog.Level) == LevelTrace {
					a.Value = slog.StringValue("TRACE")
				}
				return a
			},
		})))
	} else if cfg.LogLevel != nil && *cfg.LogLevel != "" {
		// Debug, Info, Warn, Error
		lower := strings.ToLower(*cfg.LogLevel)
		level := slog.LevelInfo

		switch lower {
		case "debug":
			level = slog.LevelDebug
		case "info":
			level = slog.LevelInfo
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		case "trace":
			level = LevelTrace
		default:
			slog.Info("Invalid logging level provided, Defaulting to Info")
		}

		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == slog.LevelKey && a.Value.Any().(slog.Level) == LevelTrace {
					a.Value = slog.StringValue("TRACE")
				}
				return a
			},
		})))
	}

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	db, err := store.Open("meshcore.db")
	if err != nil {
		slog.Error("database open failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	reconnectCh := make(chan struct{}, 1)

	ms, err := setupModem(ctx, cfg)
	if err != nil {
		slog.Error("modem setup failed", "error", err)
		os.Exit(1)
	}
	ms.startDeadWatcher(reconnectCh)

	mux := node.NewRadioMux(ms.modem, buildMuxOpts(ms)...)

	apiServer := api.NewServer(db, web.Assets(), slog.Default())
	apiServer.SetConfigPath(resolvedPath)
	apiServer.SetReloadFunc(func() error {
		p, err := os.FindProcess(os.Getpid())
		if err != nil {
			return err
		}
		return p.Signal(syscall.SIGHUP)
	})
	listenAddr := ":8080"
	if cfg.ListenAddr != nil && *cfg.ListenAddr != "" {
		listenAddr = *cfg.ListenAddr
	}
	httpServer := &http.Server{Addr: listenAddr, Handler: apiServer}
	go func() {
		slog.Info("web UI listening", "addr", listenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server error", "error", err)
		}
	}()

	wirePacketLogger(mux, db, apiServer.Hub(), apiServer)

	echoTracker := NewEchoTracker(db, apiServer.Hub(), slog.Default())

	companions, err := startCompanions(ctx, cfg, ms, mux, db, apiServer.Hub(), echoTracker)
	if err != nil {
		ms.Close()
		slog.Error("companion startup failed", "error", err)
		os.Exit(1)
	}
	setCompanionProvider(apiServer, companions)
	setChannelLookup(apiServer, companions)
	setConfigPersist(apiServer, companions)
	setConfigAPI(apiServer, cfg)

	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down...")
			httpServer.Close()
			stopCompanions(companions)
			ms.Close()
			return

		case <-sighup:
			slog.Info("SIGHUP received, reloading config...")

			newCfg, _, err := loadConfig(*configPath)
			if err != nil {
				slog.Error("config reload failed, keeping current config", "error", err)
				continue
			}

			if len(newCfg.Companions) == 0 {
				slog.Error("reloaded config has no companions, keeping current config")
				continue
			}

			stopCompanions(companions)

			if modemConfigChanged(cfg, newCfg) {
				slog.Info("modem config changed, reconnecting...")
				ms.Close()

				ms, mux, err = reconnectModem(ctx, newCfg, db, apiServer, reconnectCh)
				if err != nil {
					slog.Error("modem reconnect failed", "error", err)
					os.Exit(1)
				}
			}

			companions, err = startCompanions(ctx, newCfg, ms, mux, db, apiServer.Hub(), echoTracker)
			if err != nil {
				ms.Close()
				slog.Error("companion restart failed after reload", "error", err)
				os.Exit(1)
			}
			setCompanionProvider(apiServer, companions)
			setChannelLookup(apiServer, companions)
			setConfigPersist(apiServer, companions)
			setConfigAPI(apiServer, newCfg)

			cfg = newCfg
			slog.Info("config reloaded successfully")

		case <-reconnectCh:
			slog.Warn("modem read loop exited, reconnecting...")

			stopCompanions(companions)
			ms.Close()

			ms, mux, err = reconnectModemWithBackoff(ctx, cfg, db, apiServer, reconnectCh)
			if err != nil {
				slog.Error("modem reconnect aborted", "error", err)
				return
			}

			companions, err = startCompanions(ctx, cfg, ms, mux, db, apiServer.Hub(), echoTracker)
			if err != nil {
				ms.Close()
				slog.Error("companion restart failed after reconnect", "error", err)
				os.Exit(1)
			}
			setCompanionProvider(apiServer, companions)
			setChannelLookup(apiServer, companions)
			setConfigPersist(apiServer, companions)
			setConfigAPI(apiServer, cfg)

			slog.Info("modem reconnected")
		}
	}
}

// buildMuxOpts builds the standard mux options for the given modem state.
// Centralized so the same options are used at startup and after reconnect.
func buildMuxOpts(ms *modemState) []node.MuxOption {
	opts := []node.MuxOption{
		node.WithMuxLogger(slog.Default()),
		node.WithMuxErrorHandler(func(err error) {
			slog.Debug("mux receive error", "component", "modem", "error", err)
			ms.recvErrors.Add(1)
		}),
	}
	if ms.radioConfig != nil {
		opts = append(opts,
			node.WithMuxAirtimeEstimator(hardware.LoRaAirtimeEstimator(ms.radioConfig)),
			node.WithMuxRetryable(func(err error) bool {
				return errors.Is(err, hardware.ErrTxBusy)
			}),
		)
	}
	return opts
}

// reconnectModem performs a single setupModem attempt and rebuilds the mux,
// dead-watcher, and packet logger. Returns the new state on success.
func reconnectModem(ctx context.Context, cfg *Config, db *store.Store, srv *api.Server, reconnectCh chan struct{}) (*modemState, *node.RadioMux, error) {
	ms, err := setupModem(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	ms.startDeadWatcher(reconnectCh)
	mux := node.NewRadioMux(ms.modem, buildMuxOpts(ms)...)
	wirePacketLogger(mux, db, srv.Hub(), srv)
	return ms, mux, nil
}

// reconnectModemWithBackoff retries reconnectModem with capped exponential
// backoff until the context is cancelled. Drains spurious reconnectCh sends
// (e.g. from a half-open transport flapping) so they don't queue up.
func reconnectModemWithBackoff(ctx context.Context, cfg *Config, db *store.Store, srv *api.Server, reconnectCh chan struct{}) (*modemState, *node.RadioMux, error) {
	const (
		initialDelay = 1 * time.Second
		maxDelay     = 30 * time.Second
	)
	delay := initialDelay
	for {
		// Drain any reconnect signal queued during the previous lifetime.
		select {
		case <-reconnectCh:
		default:
		}
		ms, mux, err := reconnectModem(ctx, cfg, db, srv, reconnectCh)
		if err == nil {
			return ms, mux, nil
		}
		slog.Warn("modem reconnect attempt failed", "error", err, "retryIn", delay)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}

func modemConfigChanged(old, new_ *Config) bool {
	if derefStr(old.NodeType) != derefStr(new_.NodeType) {
		return true
	}

	switch derefStr(old.NodeType) {
	case "companion":
		return derefStr(old.Connection) != derefStr(new_.Connection) ||
			derefInt(old.BaudRate) != derefInt(new_.BaudRate)
	case "kiss":
		return derefStr(old.Connection) != derefStr(new_.Connection) ||
			derefInt(old.BaudRate) != derefInt(new_.BaudRate) ||
			derefFloat(old.Freq) != derefFloat(new_.Freq) ||
			derefFloat(old.Bw) != derefFloat(new_.Bw) ||
			derefUint8(old.SF) != derefUint8(new_.SF) ||
			derefUint8(old.CR) != derefUint8(new_.CR) ||
			derefUint8(old.TX) != derefUint8(new_.TX)
	}

	return false
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefUint8(p *uint8) uint8 {
	if p == nil {
		return 0
	}
	return *p
}

func setupModem(ctx context.Context, cfg *Config) (*modemState, error) {
	ms := &modemState{
		recvErrors: &atomic.Uint64{},
	}

	conn := *cfg.Connection
	connScheme, connAddr, ok := parseConnection(conn)
	if !ok {
		return nil, fmt.Errorf("invalid connection string: %s", conn)
	}

	switch *cfg.NodeType {
	case "kiss":
		var t hardware.Transport

		switch connScheme {
		case "serial":
			t = kissTransport.NewSerialTransport(kissTransport.SerialConfig{
				Port:     connAddr,
				BaudRate: *cfg.BaudRate,
			})
		case "tcp":
			t = kissTransport.NewTCPTransport(kissTransport.TCPConfig{
				Address: connAddr,
			})
		}

		radioConfig := &hardware.RadioConfig{
			FreqHz: uint32(*cfg.Freq * 1000000),
			BwHz:   uint32(*cfg.Bw * 1000),
			SF:     *cfg.SF,
			CR:     *cfg.CR,
		}

		kissModem := hardware.NewKissModem(
			t,
			hardware.WithSignalReport(true),
			hardware.WithLogger(slog.Default()),
		)

		connectCtx, connectCancel := context.WithTimeout(ctx, 10*time.Second)
		defer connectCancel()

		if err := kissModem.Connect(connectCtx); err != nil {
			return nil, fmt.Errorf("kiss connect: %w", err)
		}
		ms.closers = append(ms.closers, kissModem)

		ms.radioConfig = radioConfig

		if err := kissModem.SetRadio(radioConfig); err != nil {
			ms.Close()
			return nil, fmt.Errorf("SET_RADIO: %w", err)
		}
		slog.Info("SET_RADIO", "freq", *cfg.Freq, "bw", *cfg.Bw, "sf", *cfg.SF, "cr", *cfg.CR)

		if err := kissModem.SetTxPower(*cfg.TX); err != nil {
			ms.Close()
			return nil, fmt.Errorf("SET_TX_POWER: %w", err)
		}
		slog.Info("SET_TX_POWER", "tx", *cfg.TX)

		ms.stats = NewKissStatsProvider(kissModem, RadioInfo{
			FreqHz:  uint32(*cfg.Freq * 1000000),
			BwHz:    uint32(*cfg.Bw * 1000),
			SF:      *cfg.SF,
			CR:      *cfg.CR,
			TxPower: *cfg.TX,
		})

		ms.modem = kissModem

	case "companion":
		var t companionTransport.Transport

		switch connScheme {
		case "serial":
			t = companionTransport.NewSerialTransport(companionTransport.SerialConfig{
				Port:     connAddr,
				BaudRate: *cfg.BaudRate,
			})
		case "tcp":
			t = companionTransport.NewTCPTransport(companionTransport.TCPConfig{
				Address: connAddr,
			})
		}

		client := companionClient.New(t)
		ms.companionCl = client
		client.SetErrorHandler(func(err error) {
			slog.Error("companion error", "component", "modem", "error", err)
		})

		connectCtx, connectCancel := context.WithTimeout(ctx, 10*time.Second)
		defer connectCancel()

		if err := client.Connect(connectCtx); err != nil {
			return nil, fmt.Errorf("companion connect: %w", err)
		}
		ms.closers = append(ms.closers, client)

		selfInfo, err := client.AppStart(connectCtx, 1, "meshcore-bot")
		if err != nil {
			ms.Close()
			return nil, fmt.Errorf("companion app start: %w", err)
		}
		ms.stats = NewCompanionStatsProvider(client, selfInfo)

		compModem := companionClient.NewCompanionModem(ctx, client)
		ms.closers = append(ms.closers, closerFunc(compModem.Close))

		ms.modem = compModem

	default:
		return nil, fmt.Errorf("unsupported node type: %s", *cfg.NodeType)
	}

	return ms, nil
}

func startCompanions(ctx context.Context, cfg *Config, ms *modemState, mux *node.RadioMux, db *store.Store, hub *api.Hub, echoTracker *EchoTracker) ([]*Companion, error) {
	var companions []*Companion

	for _, compCfg := range cfg.Companions {
		c, err := NewCompanion(compCfg, mux, db, hub, echoTracker, ms.stats, ms.recvErrors)
		if err != nil {
			stopCompanions(companions)
			return nil, fmt.Errorf("creating companion %q: %w", compCfg.Name, err)
		}
		if err := c.Start(ctx); err != nil {
			stopCompanions(companions)
			return nil, fmt.Errorf("starting companion %q: %w", compCfg.Name, err)
		}
		companions = append(companions, c)
		slog.Info("started companion", "companion", compCfg.Name)
	}

	hydratePeerTables(db, companions)

	return companions, nil
}

func hydratePeerTables(db *store.Store, companions []*Companion) {
	if len(companions) == 0 {
		return
	}

	peers, err := db.Peers.LoadAll()
	if err != nil {
		slog.Error("failed to load peers for hydration", "error", err)
		return
	}
	if len(peers) == 0 {
		return
	}

	for _, sp := range peers {
		id, err := meshcore.NewIdentityFromBytes(sp.PubKey)
		if err != nil {
			slog.Debug("skipping peer with invalid pubkey", "error", err)
			continue
		}

		np := &node.Peer{
			Identity:            id,
			Name:                sp.Name,
			Type:                sp.Type,
			Lat:                 sp.Lat,
			Lon:                 sp.Lon,
			Feat1:               sp.Feat1,
			Feat2:               sp.Feat2,
			OutPath:             sp.OutPath,
			OutPathHashSize:     sp.OutPathHashSize,
			LastAdvertTimestamp: sp.LastAdvertTS,
			LastSeen:            sp.LastSeen,
			SNR:                 derefFloat32(sp.SNR),
			RSSI:                derefInt8(sp.RSSI),
		}

		for _, c := range companions {
			c.Node().Peers().Insert(np)
		}
	}

	slog.Info("hydrated peer tables from database", "peers", len(peers), "companions", len(companions))
}

func wirePacketLogger(mux *node.RadioMux, db *store.Store, hub *api.Hub, srv *api.Server) {
	logRadio := mux.NewRadio()

	logRadio.SetRawDataHandler(func(data []byte, snr float32, rssi int8, hasSignalInfo bool) {
		pkt, err := meshcore.PacketFromBytes(data)
		var routeType, payloadType *uint8
		if err == nil {
			rt := pkt.RouteType()
			pt := pkt.PayloadType()
			routeType = &rt
			payloadType = &pt
		}

		var snrPtr *float64
		var rssiPtr *int8
		if hasSignalInfo {
			s := float64(snr)
			snrPtr = &s
			rssiPtr = &rssi
		}

		rec := &store.PacketRecord{
			ReceivedAt:  time.Now(),
			Direction:   "rx",
			Raw:         data,
			RouteType:   routeType,
			PayloadType: payloadType,
			SNR:         snrPtr,
			RSSI:        rssiPtr,
		}

		db.WriteAsync(func() {
			if insertErr := db.Packets.Insert(rec); insertErr != nil {
				slog.Debug("failed to log rx packet", "error", insertErr)
			}
		})

		msg := packetBroadcastMsg("rx", rec.ReceivedAt, data, pkt, err, srv.ChannelLookup())
		if hasSignalInfo {
			msg["snr"] = snr
			msg["rssi"] = rssi
		}
		hub.Broadcast("packets", msg)
	})

	logRadio.AddOutboundHandler(func(data []byte) {
		pkt, err := meshcore.PacketFromBytes(data)
		var routeType, payloadType *uint8
		if err == nil {
			rt := pkt.RouteType()
			pt := pkt.PayloadType()
			routeType = &rt
			payloadType = &pt
		}

		rec := &store.PacketRecord{
			ReceivedAt:  time.Now(),
			Direction:   "tx",
			Raw:         data,
			RouteType:   routeType,
			PayloadType: payloadType,
		}

		db.WriteAsync(func() {
			if insertErr := db.Packets.Insert(rec); insertErr != nil {
				slog.Debug("failed to log tx packet", "error", insertErr)
			}
		})

		hub.Broadcast("packets", packetBroadcastMsg("tx", rec.ReceivedAt, data, pkt, err, srv.ChannelLookup()))
	})
}

func packetBroadcastMsg(direction string, receivedAt time.Time, data []byte, pkt *meshcore.Packet, parseErr error, channels api.ChannelLookup) map[string]any {
	msg := map[string]any{
		"direction":  direction,
		"receivedAt": receivedAt.Format(time.RFC3339),
		"raw":        hex.EncodeToString(data),
	}
	if parseErr != nil {
		return msg
	}
	rt := pkt.RouteType()
	pt := pkt.PayloadType()
	msg["routeType"] = rt
	msg["payloadType"] = pt
	msg["route"] = pkt.RouteTypeString()
	msg["pathHashSize"] = pkt.PathHashSize()
	msg["hops"] = pkt.PathHashCount()
	ph := pkt.PacketHash()
	msg["packetHash"] = hex.EncodeToString(ph[:])
	msg["summary"] = api.PacketSummary(pkt, channels)
	return msg
}

func setCompanionProvider(srv *api.Server, companions []*Companion) {
	srv.SetCompanionProvider(func() []api.CompanionInfo {
		infos := make([]api.CompanionInfo, 0, len(companions))
		for _, c := range companions {
			channels := make([]api.ChannelInfo, 0)
			for _, ch := range c.Node().Channels() {
				channels = append(channels, api.ChannelInfo{
					Name: ch.Name,
					PSK:  ch.PSK[:],
				})
			}
			infos = append(infos, api.CompanionInfo{
				Name:      c.Name(),
				PubKey:    hex.EncodeToString(c.Node().Identity().Identity.PublicKeyBytes()),
				PeerCount: c.Node().Peers().Count(),
				Channels:  channels,
			})
		}
		return infos
	})

	srv.SetCompanionLookup(func(name string) (api.MessageSender, api.DMSender, bool) {
		for _, c := range companions {
			if c.Name() == name {
				return c.SendChannelMessage, c.SendContactMessage, true
			}
		}
		return nil, nil, false
	})

	srv.SetChannelMutator(func(name string) (api.ChannelAdder, api.ChannelRemover, bool) {
		for _, c := range companions {
			if c.Name() == name {
				adder := func(chName, privateKey string) error {
					return c.AddChannel(ChannelRef{Name: chName, PrivateKey: privateKey})
				}
				return adder, c.RemoveChannel, true
			}
		}
		return nil, nil, false
	})

	srv.SetChannelRenamer(func(companionName, oldName, newName string) error {
		for _, c := range companions {
			if c.Name() == companionName {
				return c.RenameChannel(oldName, newName)
			}
		}
		return fmt.Errorf("companion %q not found", companionName)
	})

	srv.SetTraceSenderLookup(func(name string) (api.TraceSender, bool) {
		for _, c := range companions {
			if c.Name() == name {
				return c.SendTrace, true
			}
		}
		return nil, false
	})

	srv.SetRepeaterLookup(func(name string) (*api.RepeaterOps, bool) {
		for _, c := range companions {
			if c.Name() == name {
				rm := c.repeaters
				return &api.RepeaterOps{
					Login: func(pubkeyHex, password string) (any, error) {
						return rm.SendLogin(pubkeyHex, password, 10*time.Second)
					},
					StatusReq: func(pubkeyHex string) (any, error) {
						return rm.SendStatusReq(pubkeyHex, 10*time.Second)
					},
					CLI: func(pubkeyHex, command string) (string, error) {
						return rm.SendCLI(pubkeyHex, command, 10*time.Second)
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
						return rm.SendNeighborsReq(pubkeyHex, count, offset, 10*time.Second)
					},
					OwnerInfoReq: func(pubkeyHex string) (any, error) {
						return rm.SendOwnerInfoReq(pubkeyHex, 10*time.Second)
					},
					TelemetryReq: func(pubkeyHex string) (any, error) {
						return rm.SendTelemetryReq(pubkeyHex, 10*time.Second)
					},
					AccessList: func(pubkeyHex string) (any, error) {
						return rm.SendAccessListReq(pubkeyHex, 10*time.Second)
					},
					SetPerm: func(pubkeyHex, targetPubkeyHex string, perms uint8) error {
						return rm.SetAccessPerm(pubkeyHex, targetPubkeyHex, perms, 10*time.Second)
					},
				}, true
			}
		}
		return nil, false
	})
}

func setChannelLookup(srv *api.Server, companions []*Companion) {
	byHash := make(map[byte]*api.ChannelInfo)
	for _, c := range companions {
		for _, ch := range c.Node().Channels() {
			if ch == nil {
				continue
			}
			byHash[ch.Hash] = &api.ChannelInfo{Name: ch.Name, PSK: ch.PSK[:]}
		}
	}
	srv.SetChannelLookup(func(hash byte) *api.ChannelInfo {
		return byHash[hash]
	})
}

func stopCompanions(companions []*Companion) {
	for _, comp := range companions {
		comp.Stop()
	}
}

func setConfigPersist(srv *api.Server, companions []*Companion) {
	srv.SetConfigPersist(func() error {
		cfgPath := srv.ConfigPath()
		if cfgPath == "" {
			return fmt.Errorf("config path not set")
		}

		cfg, _, err := loadConfigFromPath(cfgPath)
		if err != nil {
			return fmt.Errorf("reading config for persist: %w", err)
		}

		for i, comp := range companions {
			if i >= len(cfg.Companions) {
				break
			}
			channels := standaloneChannels(comp)
			if len(channels) > 0 {
				cl := ChannelList(channels)
				cfg.Companions[i].Channels = &cl
			} else {
				cfg.Companions[i].Channels = nil
			}
		}

		data, err := marshalConfig(cfgPath, cfg)
		if err != nil {
			return fmt.Errorf("marshalling config: %w", err)
		}

		if err := os.WriteFile(cfgPath, data, 0644); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		setChannelLookup(srv, companions)

		slog.Info("config persisted with channel changes")
		return nil
	})
}

func setConfigAPI(srv *api.Server, cfg *Config) {
	srv.SetConfigGetter(func() any {
		cfgPath := srv.ConfigPath()
		if cfgPath == "" {
			return cfg
		}
		current, _, err := loadConfigFromPath(cfgPath)
		if err != nil {
			return cfg
		}
		return current
	})

	srv.SetConfigUpdater(func(input map[string]any) error {
		cfgPath := srv.ConfigPath()
		if cfgPath == "" {
			return fmt.Errorf("config path not set")
		}

		raw, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("invalid config data: %w", err)
		}

		var newCfg Config
		if err := json.Unmarshal(raw, &newCfg); err != nil {
			return fmt.Errorf("config validation failed: %w", err)
		}

		if err := validateConfig(&newCfg); err != nil {
			return err
		}

		newCfg.applyDefaults()

		data, err := marshalConfig(cfgPath, &newCfg)
		if err != nil {
			return fmt.Errorf("failed to encode config: %w", err)
		}

		if err := os.WriteFile(cfgPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write config: %w", err)
		}

		reloadFn := srv.ReloadFunc()
		if reloadFn != nil {
			if err := reloadFn(); err != nil {
				return fmt.Errorf("config saved but reload failed: %w", err)
			}
		}

		slog.Info("config updated via API")
		return nil
	})
}

func validateConfig(cfg *Config) error {
	if cfg.NodeType != nil {
		switch *cfg.NodeType {
		case "kiss", "companion":
		default:
			return fmt.Errorf("invalid nodeType %q: must be \"kiss\" or \"companion\"", *cfg.NodeType)
		}
	}

	if cfg.Connection != nil {
		_, _, ok := parseConnection(*cfg.Connection)
		if !ok {
			return fmt.Errorf("invalid connection string %q: must start with serial:// or tcp://", *cfg.Connection)
		}
	}

	if cfg.BaudRate != nil && *cfg.BaudRate <= 0 {
		return fmt.Errorf("baudRate must be positive")
	}

	if cfg.Freq != nil && (*cfg.Freq < 100 || *cfg.Freq > 1000) {
		return fmt.Errorf("freq must be between 100 and 1000 MHz")
	}

	if cfg.Bw != nil && *cfg.Bw <= 0 {
		return fmt.Errorf("bw must be positive")
	}

	if cfg.SF != nil && (*cfg.SF < 5 || *cfg.SF > 12) {
		return fmt.Errorf("sf must be between 5 and 12")
	}

	if cfg.CR != nil && (*cfg.CR < 5 || *cfg.CR > 8) {
		return fmt.Errorf("cr must be between 5 and 8")
	}

	if cfg.TX != nil && *cfg.TX > 22 {
		return fmt.Errorf("tx must be between 0 and 22 dBm")
	}

	if len(cfg.Companions) == 0 {
		return fmt.Errorf("at least one companion must be configured")
	}

	for i, comp := range cfg.Companions {
		if comp.Name == "" {
			return fmt.Errorf("companion[%d]: name is required", i)
		}
		if comp.KeyFile == "" {
			return fmt.Errorf("companion[%d] %q: keyFile is required", i, comp.Name)
		}
	}

	return nil
}

func standaloneChannels(c *Companion) []ChannelRef {
	allChs := c.Node().Channels()
	var refs []ChannelRef
	for i := c.triggerChannelCount; i < len(allChs); i++ {
		ch := allChs[i]
		if ch == nil {
			continue
		}
		ref := ChannelRef{Name: ch.Name}
		if !isHashtagChannel(ch) {
			ref.PrivateKey = hex.EncodeToString(ch.PSK[:])
		}
		refs = append(refs, ref)
	}
	return refs
}

func isHashtagChannel(ch *meshcore.ChannelEntry) bool {
	if strings.HasPrefix(ch.Name, "#") {
		derived := meshcore.NewChannelFromHashtag(meshcore.NormalizeHashtag(ch.Name))
		return derived.Hash == ch.Hash
	}
	if strings.EqualFold(ch.Name, "Public") {
		pub, err := meshcore.NewChannelFromBase64("Public", "izOH6cXN6mrJ5e26oRXNcg==")
		if err != nil {
			return false
		}
		return pub.Hash == ch.Hash
	}
	return false
}

func marshalConfig(path string, cfg *Config) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".toml":
		return toml.Marshal(cfg)
	case ".yaml", ".yml":
		return yaml.Marshal(cfg)
	case ".json":
		return json.MarshalIndent(cfg, "", "  ")
	default:
		return nil, fmt.Errorf("unsupported config format %q", ext)
	}
}

func loadConfig(path string) (*Config, string, error) {
	if path == "" {
		return loadConfigFromCwd()
	}
	return loadConfigFromPath(path)
}

func loadConfigFromCwd() (*Config, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("getting working directory: %w", err)
	}

	for _, name := range defaultConfigNames {
		p := filepath.Join(cwd, name)
		if _, err := os.Stat(p); err == nil {
			slog.Info("using config", "path", p)
			return loadConfigFromPath(p)
		}
	}

	return nil, "", fmt.Errorf("no config file found in %s (tried %s)", cwd, strings.Join(defaultConfigNames, ", "))
}

func loadConfigFromPath(path string) (*Config, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("reading config: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".toml":
		cfg, err := UnmarshalConfigToml(data)
		return cfg, path, err
	case ".yaml", ".yml":
		cfg, err := UnmarshalConfigYaml(data)
		return cfg, path, err
	case ".json":
		cfg, err := UnmarshalConfigJson(data)
		return cfg, path, err
	default:
		return nil, path, fmt.Errorf("unsupported config format %q", ext)
	}
}

func parseConnection(conn string) (scheme, addr string, ok bool) {
	for _, prefix := range []string{"serial://", "tcp://"} {
		if strings.HasPrefix(conn, prefix) {
			return strings.TrimSuffix(prefix, "://"), conn[len(prefix):], true
		}
	}
	return "", "", false
}

func derefInt8(p *int8) int8 {
	if p == nil {
		return 0
	}
	return *p
}

func derefFloat32(p *float64) float32 {
	if p == nil {
		return 0
	}
	return float32(*p)
}
